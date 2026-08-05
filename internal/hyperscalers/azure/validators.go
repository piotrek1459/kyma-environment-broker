package azure

import (
	"context"
	"fmt"

	azurecloud "github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/kyma-project/kyma-environment-broker/common/gardener"
)

// AzureBindingValidator validates a CredentialsBinding by fetching its secret,
// extracting Azure credentials, and probing the Azure cloud (public/china/gov).
// Stores DiscoveredCloud and DiscoveredCreds so callers avoid a second fetch.
//
// cloudProbe allows injecting a fake resolver in tests; nil means use ResolveCloudConfig.
type AzureBindingValidator struct {
	GardenerClient  *gardener.Client
	DiscoveredCloud azurecloud.Configuration
	DiscoveredCreds AzureCredentials
	// cloudProbe is injectable for tests; nil → real ResolveCloudConfig
	cloudProbe func(ctx context.Context, creds AzureCredentials) (azurecloud.Configuration, error)
}

func (v *AzureBindingValidator) Validate(ctx context.Context, cb *gardener.CredentialsBinding) error {
	secret, err := v.GardenerClient.GetSecret(cb.GetSecretRefNamespace(), cb.GetSecretRefName())
	if err != nil {
		return fmt.Errorf("unable to get secret %s/%s: %w", cb.GetSecretRefNamespace(), cb.GetSecretRefName(), err)
	}
	creds, err := ExtractCredentials(secret)
	if err != nil {
		return err
	}
	probe := v.cloudProbe
	if probe == nil {
		probe = ResolveCloudConfig
	}
	cloudCfg, err := probe(ctx, creds)
	if err != nil {
		return err
	}
	v.DiscoveredCloud = cloudCfg
	v.DiscoveredCreds = creds
	return nil
}

