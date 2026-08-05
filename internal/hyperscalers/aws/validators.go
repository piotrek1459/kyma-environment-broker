package aws

import (
	"context"
	"fmt"

	"github.com/kyma-project/kyma-environment-broker/common/gardener"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// AWSBindingValidator validates a CredentialsBinding by fetching its secret and
// verifying the credentials can be extracted (correct format and base64 encoding).
// After successful Validate, DiscoveredSecret returns the fetched secret so callers
// avoid a second GetSecret call.
//
// Unlike AzureBindingValidator, no live API probe is performed — AWS credentials
// are validated lazily on the first EC2/STS API call. A format-only check here is
// sufficient to detect missing or malformed secrets before they propagate further.
type AWSBindingValidator struct {
	GardenerClient   *gardener.Client
	Region           string
	discoveredSecret *unstructured.Unstructured
}

func (v *AWSBindingValidator) Validate(ctx context.Context, cb *gardener.CredentialsBinding) error {
	secret, err := v.GardenerClient.GetSecret(cb.GetSecretRefNamespace(), cb.GetSecretRefName())
	if err != nil {
		return fmt.Errorf("unable to get secret %s/%s: %w", cb.GetSecretRefNamespace(), cb.GetSecretRefName(), err)
	}
	if _, _, err := ExtractCredentials(secret); err != nil {
		return err
	}
	v.discoveredSecret = secret
	return nil
}

func (v *AWSBindingValidator) DiscoveredSecret() *unstructured.Unstructured {
	return v.discoveredSecret
}
