package hyperscalers

import (
	"context"
	"fmt"

	pkg "github.com/kyma-project/kyma-environment-broker/common/runtime"
	"github.com/kyma-project/kyma-environment-broker/internal/hyperscalers/aws"
	"github.com/kyma-project/kyma-environment-broker/internal/hyperscalers/azure"
	"github.com/kyma-project/kyma-environment-broker/internal/provider/configuration"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type hyperscalerFactory struct {
	providerSpec *configuration.ProviderSpec
	azureCache   *azure.AzureCache
}

// NewFactory creates a new Factory. If an azureSecret is provided and Azure has zonesDiscovery
// enabled, a global background cache is started for all configured Azure regions.
func NewFactory(providerSpec *configuration.ProviderSpec) Factory {
	return &hyperscalerFactory{providerSpec: providerSpec}
}

// NewFactoryWithAzureCache creates a Factory with a global Azure zone cache.
// The cache fills lazily in the background — KEB startup is not blocked.
func NewFactoryWithAzureCache(ctx context.Context, providerSpec *configuration.ProviderSpec, azureSecret *unstructured.Unstructured) Factory {
	var azureCache *azure.AzureCache
	if azureSecret != nil && providerSpec.ZonesDiscovery(pkg.Azure) {
		azureCache = azure.NewAzureCache(ctx, providerSpec, azureSecret)
	}
	return &hyperscalerFactory{
		providerSpec: providerSpec,
		azureCache:   azureCache,
	}
}

func (f *hyperscalerFactory) NewFromSecret(ctx context.Context, provider pkg.CloudProvider, secret *unstructured.Unstructured, region string) (ProviderClient, error) {
	switch provider {
	case pkg.AWS:
		return aws.NewClientFromSecret(ctx, f.providerSpec, secret, region)
	case pkg.Azure:
		// Use global cache if available and ready for this region — zero latency.
		// Falls back to per-call client if cache is not yet ready (lazy fill in progress).
		if f.azureCache != nil && f.azureCache.Ready(region) {
			return azure.NewCachedClient(f.azureCache, region, f.providerSpec), nil
		}
		return azure.NewClientFromSecret(ctx, f.providerSpec, secret, region)
	default:
		return nil, fmt.Errorf("zone discovery not supported for provider %s", provider)
	}
}
