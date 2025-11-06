package steps

import (
	"testing"

	"github.com/kyma-project/kyma-environment-broker/common/runtime"
	"github.com/kyma-project/kyma-environment-broker/internal"
	"github.com/kyma-project/kyma-environment-broker/internal/fixture"
	"github.com/kyma-project/kyma-environment-broker/internal/ptr"
	"github.com/kyma-project/kyma-environment-broker/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestInitKymaTemplate_Run(t *testing.T) {
	// given
	db := storage.NewMemoryStorage()
	operation := fixture.FixOperation("op-id", "inst-id", internal.OperationTypeProvision)
	err := db.Operations().InsertOperation(operation)
	require.NoError(t, err)

	svc := NewInitKymaTemplate(db.Operations(), &fakeConfigProvider{})

	// when
	op, backoff, err := svc.Run(operation, fixLogger())
	require.NoError(t, err)

	// then
	assert.Zero(t, backoff)
	assert.Equal(t, "kyma-system", op.KymaResourceNamespace)
	assert.NotEmptyf(t, op.KymaTemplate, "KymaTemplate should not be empty")
}

func TestInitKymaTemplate_ApplyChannelToTemplate(t *testing.T) {
	tests := []struct {
		name            string
		userChannel     *string
		expectedChannel string
		expectError     bool
	}{
		{
			name:            "applies user channel 'fast'",
			userChannel:     ptr.String("fast"),
			expectedChannel: "fast",
			expectError:     false,
		},
		{
			name:            "applies user channel 'regular'",
			userChannel:     ptr.String("regular"),
			expectedChannel: "regular",
			expectError:     false,
		},
		{
			name:            "keeps template default when no user channel",
			userChannel:     nil,
			expectedChannel: "stable", // from template
			expectError:     false,
		},
		{
			name:            "keeps template default when empty user channel",
			userChannel:     ptr.String(""),
			expectedChannel: "stable", // from template
			expectError:     false,
		},
		{
			name:        "fails with invalid channel",
			userChannel: ptr.String("invalid"),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// given
			db := storage.NewMemoryStorage()
			operation := fixture.FixOperation("op-id", "inst-id", internal.OperationTypeProvision)

			// Set up modules with channel
			if tt.userChannel != nil {
				operation.ProvisioningParameters.Parameters.Modules = &runtime.ModulesDTO{
					Channel: tt.userChannel,
					Default: ptr.Bool(true),
				}
			}

			err := db.Operations().InsertOperation(operation)
			require.NoError(t, err)

			svc := NewInitKymaTemplate(db.Operations(), &fakeConfigProvider{})

			// when
			op, backoff, err := svc.Run(operation, fixLogger())

			// then
			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Zero(t, backoff)
			assert.Equal(t, "kyma-system", op.KymaResourceNamespace)
			assert.NotEmpty(t, op.KymaTemplate)

			// Verify the channel was applied correctly
			decoded, decodeErr := DecodeKymaTemplate(op.KymaTemplate)
			require.NoError(t, decodeErr)

			actualChannel, found, getErr := unstructured.NestedString(decoded.Object, "spec", "channel")
			require.NoError(t, getErr)
			require.True(t, found, "channel should be present in template")
			assert.Equal(t, tt.expectedChannel, actualChannel)
		})
	}
}

type fakeConfigProvider struct{}

func (f fakeConfigProvider) Provide(cfgKeyName string, cfgDestObj any) error {
	cfg, _ := cfgDestObj.(*internal.ConfigForPlan)
	cfg.KymaTemplate = `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
metadata:
    name: my-kyma
    namespace: kyma-system
spec:
    sync:
        strategy: secret
    channel: stable
    modules: []
`
	cfgDestObj = cfg
	return nil
}
