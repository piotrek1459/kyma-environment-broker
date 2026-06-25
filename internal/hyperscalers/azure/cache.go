package azure

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	pkg "github.com/kyma-project/kyma-environment-broker/common/runtime"
	"github.com/kyma-project/kyma-environment-broker/internal/provider/configuration"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	refreshInterval      = 1 * time.Hour
	cacheRetries         = 3
	cacheRetryInterval   = 2 * time.Second
)

// AzureCache is a global singleton cache of available zones per region and machine type.
// It is filled lazily in the background after KEB starts and refreshed every hour.
// Only regions and machine types configured in providerSpec are cached.
type AzureCache struct {
	mu           sync.RWMutex
	data         map[string]map[string][]string // region → machineType → zones
	providerSpec *configuration.ProviderSpec
}

// NewAzureCache creates a new AzureCache and starts a background goroutine that fills
// all configured regions and refreshes them every hour. It does not block startup.
func NewAzureCache(ctx context.Context, providerSpec *configuration.ProviderSpec, secret *unstructured.Unstructured) *AzureCache {
	c := &AzureCache{
		data:         make(map[string]map[string][]string),
		providerSpec: providerSpec,
	}
	go c.run(ctx, secret)
	return c
}

// ZonesFor returns available zones for the given region and machine type.
// Returns nil if the cache is not yet ready for the region.
func (c *AzureCache) ZonesFor(region, machineType string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	regionCache, ok := c.data[region]
	if !ok {
		return nil
	}
	return regionCache[machineType]
}

// Ready reports whether the cache has been filled for the given region.
func (c *AzureCache) Ready(region string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.data[region]
	return ok
}

func (c *AzureCache) run(ctx context.Context, secret *unstructured.Unstructured) {
	c.fillAll(ctx, secret)

	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.fillAll(ctx, secret)
		case <-ctx.Done():
			return
		}
	}
}

func (c *AzureCache) fillAll(ctx context.Context, secret *unstructured.Unstructured) {
	regions := c.providerSpec.Regions(pkg.Azure)
	for _, region := range regions {
		var lastErr error
		for i := 0; i < cacheRetries; i++ {
			if err := c.fillRegion(ctx, secret, region); err == nil {
				break
			} else {
				lastErr = err
				time.Sleep(cacheRetryInterval)
			}
		}
		if lastErr != nil {
			slog.Error(fmt.Sprintf("failed to fill Azure zone cache for region %s after %d retries: %s", region, cacheRetries, lastErr))
		}
	}
}

func (c *AzureCache) fillRegion(ctx context.Context, secret *unstructured.Unstructured, region string) error {
	slog.Info(fmt.Sprintf("filling Azure zone cache for region %s", region))

	clientID, clientSecret, tenantID, subscriptionID, err := ExtractCredentials(secret)
	if err != nil {
		return fmt.Errorf("failed to extract Azure credentials: %w", err)
	}

	credential, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
	if err != nil {
		return fmt.Errorf("while creating Azure credential: %w", err)
	}

	skusClient, err := armcompute.NewResourceSKUsClient(subscriptionID, credential, nil)
	if err != nil {
		return fmt.Errorf("while creating Azure ResourceSKUs client: %w", err)
	}

	supported := make(map[string]struct{})
	for _, mt := range c.providerSpec.MachineTypes(pkg.Azure) {
		supported[mt] = struct{}{}
	}

	regionCache := make(map[string][]string)
	filter := fmt.Sprintf("location eq '%s'", region)
	pager := skusClient.NewListPager(&armcompute.ResourceSKUsClientListOptions{Filter: &filter})

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list Azure resource SKUs for region %s: %w", region, err)
		}
		for _, sku := range page.Value {
			if sku.ResourceType == nil || *sku.ResourceType != "virtualMachines" || sku.Name == nil {
				continue
			}
			if _, ok := supported[*sku.Name]; !ok {
				continue
			}
			regionCache[*sku.Name] = availableZonesFromSKU(sku)
		}
	}

	c.mu.Lock()
	c.data[region] = regionCache
	c.mu.Unlock()

	slog.Info(fmt.Sprintf("Azure zone cache filled for region %s (%d machine types)", region, len(regionCache)))
	return nil
}

// AzureCachedClient implements hyperscalers.ProviderClient using the global AzureCache.
// Used for HTTP validation and machines_availability — zero latency, no API calls.
type AzureCachedClient struct {
	cache        *AzureCache
	region       string
	providerSpec *configuration.ProviderSpec
}

func NewCachedClient(cache *AzureCache, region string, providerSpec *configuration.ProviderSpec) *AzureCachedClient {
	return &AzureCachedClient{cache: cache, region: region, providerSpec: providerSpec}
}

func (c *AzureCachedClient) AvailableZones(_ context.Context, machineType string) ([]string, error) {
	machineType = c.providerSpec.ResolveMachineType(pkg.Azure, machineType)
	return c.cache.ZonesFor(c.region, machineType), nil
}

func (c *AzureCachedClient) AvailableZonesCount(ctx context.Context, machineType string) (int, error) {
	zones, err := c.AvailableZones(ctx, machineType)
	return len(zones), err
}
