package gardener

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// SecretAccessValidator is a provider-agnostic BindingValidator that checks
// GetSecret succeeds and credentials can be extracted. Used by handler_cb and
// newHyperscalerClient where live cloud probes are not needed — the secret is
// the only thing required to build a client or use a warm cache.
// After successful Validate, Secret holds the fetched secret for reuse.
type SecretAccessValidator struct {
	GardenerClient *Client
	Secret         *unstructured.Unstructured
}

func (v *SecretAccessValidator) Validate(ctx context.Context, cb *CredentialsBinding) error {
	secret, err := v.GardenerClient.GetSecret(cb.GetSecretRefNamespace(), cb.GetSecretRefName())
	if err != nil {
		return fmt.Errorf("unable to get secret %s/%s: %w", cb.GetSecretRefNamespace(), cb.GetSecretRefName(), err)
	}
	if len(secret.Object) == 0 {
		return fmt.Errorf("secret %s/%s is empty", cb.GetSecretRefNamespace(), cb.GetSecretRefName())
	}
	v.Secret = secret
	return nil
}
