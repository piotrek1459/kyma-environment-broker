package hyperscalers

import (
	pkg "github.com/kyma-project/kyma-environment-broker/common/runtime"
	"github.com/kyma-project/kyma-environment-broker/common/gardener"
	awshyperscaler "github.com/kyma-project/kyma-environment-broker/internal/hyperscalers/aws"
	azurehyperscaler "github.com/kyma-project/kyma-environment-broker/internal/hyperscalers/azure"
)

// NewBindingValidator returns a BindingValidator for the given cloud provider.
// Azure: probes cloud (public/china/gov) + extracts credentials — stores DiscoveredCloud and DiscoveredCreds.
// AWS:   calls sts.GetCallerIdentity as an eager probe — stores DiscoveredSecret.
// Panics for unsupported providers — callers must only call this for providers with zones discovery enabled.
func NewBindingValidator(provider pkg.CloudProvider, gardenerClient *gardener.Client, region string) gardener.BindingValidator {
	switch provider {
	case pkg.Azure:
		return &azurehyperscaler.AzureBindingValidator{GardenerClient: gardenerClient}
	case pkg.AWS:
		return &awshyperscaler.AWSBindingValidator{GardenerClient: gardenerClient, Region: region}
	default:
		panic("NewBindingValidator: unsupported provider " + string(provider))
	}
}
