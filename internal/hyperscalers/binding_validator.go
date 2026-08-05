package hyperscalers

import (
	"fmt"

	pkg "github.com/kyma-project/kyma-environment-broker/common/runtime"
	"github.com/kyma-project/kyma-environment-broker/common/gardener"
	awshyperscaler "github.com/kyma-project/kyma-environment-broker/internal/hyperscalers/aws"
	azurehyperscaler "github.com/kyma-project/kyma-environment-broker/internal/hyperscalers/azure"
)

// NewBindingValidator returns a BindingValidator for the given cloud provider.
// Azure: probes cloud (public/china/gov) + extracts credentials — stores DiscoveredCloud and DiscoveredCreds.
// AWS:   format-checks credentials — stores DiscoveredSecret.
// Returns an error for unsupported providers instead of panicking.
func NewBindingValidator(provider pkg.CloudProvider, gardenerClient *gardener.Client, region string) (gardener.BindingValidator, error) {
	switch provider {
	case pkg.Azure:
		return &azurehyperscaler.AzureBindingValidator{GardenerClient: gardenerClient}, nil
	case pkg.AWS:
		return &awshyperscaler.AWSBindingValidator{GardenerClient: gardenerClient, Region: region}, nil
	default:
		return nil, fmt.Errorf("NewBindingValidator: unsupported provider %q", provider)
	}
}
