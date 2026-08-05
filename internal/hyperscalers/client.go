package hyperscalers

import (
	"context"

	"github.com/kyma-project/kyma-environment-broker/common/gardener"
	pkg "github.com/kyma-project/kyma-environment-broker/common/runtime"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type ProviderClient interface {
	AvailableZones(ctx context.Context, machineType string) ([]string, error)
	AvailableZonesCount(ctx context.Context, machineType string) (int, error)
}

type Factory interface {
	NewFromSecret(ctx context.Context, provider pkg.CloudProvider, secret *unstructured.Unstructured, region string) (ProviderClient, error)
	// NewPerCallFromSecret always creates a fresh per-call client, bypassing any global cache.
	// Used by the async DiscoverAvailableZonesCBStep to ensure zone discovery uses
	// the exact Kyma-specific subscription secret, not the global cache startup secret.
	NewPerCallFromSecret(ctx context.Context, provider pkg.CloudProvider, secret *unstructured.Unstructured, region string) (ProviderClient, error)
	// NewBindingValidator returns a validator appropriate for the given provider.
	// Callers use it with gardener.FindValidBinding to select a working CredentialsBinding.
	// Returns an error for unsupported providers.
	NewBindingValidator(provider pkg.CloudProvider, gardenerClient *gardener.Client, region string) (gardener.BindingValidator, error)
}
