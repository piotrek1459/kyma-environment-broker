package azure

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	pkg "github.com/kyma-project/kyma-environment-broker/common/runtime"
	"github.com/kyma-project/kyma-environment-broker/internal/provider/configuration"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type ResourceSKUsAPI interface {
	NewListPager(options *armcompute.ResourceSKUsClientListOptions) *runtime.Pager[armcompute.ResourceSKUsClientListResponse]
}

type AzureClient struct {
	skusClient   ResourceSKUsAPI
	region       string
	providerSpec *configuration.ProviderSpec
	cache        map[string][]string
	cacheDone    bool
}

func NewClientFromSecret(ctx context.Context, providerSpec *configuration.ProviderSpec, secret *unstructured.Unstructured, region string) (*AzureClient, error) {
	clientID, clientSecret, tenantID, subscriptionID, err := ExtractCredentials(secret)
	if err != nil {
		return nil, fmt.Errorf("failed to extract Azure credentials: %w", err)
	}

	credential, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
	if err != nil {
		return nil, fmt.Errorf("while creating Azure credential: %w", err)
	}

	skusClient, err := armcompute.NewResourceSKUsClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("while creating Azure ResourceSKUs client: %w", err)
	}

	return &AzureClient{
		skusClient:   skusClient,
		region:       region,
		providerSpec: providerSpec,
	}, nil
}

func (c *AzureClient) AvailableZones(ctx context.Context, machineType string) ([]string, error) {
	machineType = c.providerSpec.ResolveMachineType(pkg.Azure, machineType)

	if err := c.fillCache(ctx); err != nil {
		return nil, err
	}

	return c.cache[machineType], nil
}

func (c *AzureClient) AvailableZonesCount(ctx context.Context, machineType string) (int, error) {
	zones, err := c.AvailableZones(ctx, machineType)
	if err != nil {
		return 0, err
	}
	return len(zones), nil
}

func (c *AzureClient) fillCache(ctx context.Context) error {
	if c.cacheDone {
		return nil
	}

	c.cache = make(map[string][]string)

	filter := fmt.Sprintf("location eq '%s'", c.region)
	pager := c.skusClient.NewListPager(&armcompute.ResourceSKUsClientListOptions{
		Filter: &filter,
	})

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list Azure resource SKUs: %w", err)
		}

		for _, sku := range page.Value {
			if sku.ResourceType == nil || *sku.ResourceType != "virtualMachines" {
				continue
			}
			if sku.Name == nil {
				continue
			}

			c.cache[*sku.Name] = availableZonesFromSKU(sku)
		}
	}

	c.cacheDone = true
	return nil
}

// availableZonesFromSKU returns zones where the SKU is available,
// excluding zones with zone-type restrictions.
func availableZonesFromSKU(sku *armcompute.ResourceSKU) []string {
	if len(sku.LocationInfo) == 0 {
		return nil
	}

	available := make(map[string]struct{})
	for _, z := range sku.LocationInfo[0].Zones {
		if z != nil {
			available[*z] = struct{}{}
		}
	}

	for _, restriction := range sku.Restrictions {
		if restriction.Type == nil || *restriction.Type != armcompute.ResourceSKURestrictionsTypeZone {
			continue
		}
		if restriction.RestrictionInfo == nil {
			continue
		}
		for _, z := range restriction.RestrictionInfo.Zones {
			if z != nil {
				delete(available, *z)
			}
		}
	}

	zones := make([]string, 0, len(available))
	for z := range available {
		zones = append(zones, z)
	}
	return zones
}

func ExtractCredentials(secret *unstructured.Unstructured) (clientID, clientSecret, tenantID, subscriptionID string, err error) {
	data, found, err := unstructured.NestedStringMap(secret.Object, "data")
	if err != nil {
		return "", "", "", "", fmt.Errorf("unable to extract data from secret: %w", err)
	}
	if !found {
		return "", "", "", "", fmt.Errorf("secret does not contain data")
	}

	clientID, err = decodeField(data, "clientID")
	if err != nil {
		return "", "", "", "", err
	}
	clientSecret, err = decodeField(data, "clientSecret")
	if err != nil {
		return "", "", "", "", err
	}
	tenantID, err = decodeField(data, "tenantID")
	if err != nil {
		return "", "", "", "", err
	}
	subscriptionID, err = decodeField(data, "subscriptionID")
	if err != nil {
		return "", "", "", "", err
	}

	return clientID, clientSecret, tenantID, subscriptionID, nil
}

func decodeField(data map[string]string, field string) (string, error) {
	raw, ok := data[field]
	if !ok {
		return "", fmt.Errorf("secret does not contain %s", field)
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", fmt.Errorf("failed to decode %s: %w", field, err)
	}
	return string(decoded), nil
}
