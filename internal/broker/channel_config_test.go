package broker

import (
	"testing"

	"github.com/kyma-project/kyma-environment-broker/internal/broker/testutil"
	"github.com/kyma-project/kyma-environment-broker/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetChannelFromPlanConfig_DefaultPlan(t *testing.T) {
	// given
	configProvider := config.NewConfigMapConfigProvider(
		testutil.NewFakeConfigProviderWithData(map[string]map[string]interface{}{
			"default": {
				"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
metadata:
  name: my-kyma
spec:
  channel: stable
  modules:
    - name: btp-operator
`,
			},
		}),
		"test-config-map",
		config.RuntimeConfigurationRequiredFields,
	)

	// when
	channel, err := GetChannelFromPlanConfig(configProvider, DefaultPlanName)

	// then
	require.NoError(t, err)
	assert.Equal(t, "stable", channel)
}

func TestGetChannelFromPlanConfig_PlanSpecificChannel(t *testing.T) {
	// given
	configProvider := config.NewConfigMapConfigProvider(
		testutil.NewFakeConfigProviderWithData(map[string]map[string]interface{}{
			"default": {
				"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
metadata:
  name: my-kyma
spec:
  channel: stable
  modules:
    - name: btp-operator
`,
			},
			"aws": {
				"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
metadata:
  name: my-kyma
spec:
  channel: fast
  modules:
    - name: btp-operator
    - name: istio
`,
			},
		}),
		"test-config-map",
		config.RuntimeConfigurationRequiredFields,
	)

	// when - get channel from aws plan
	awsChannel, err := GetChannelFromPlanConfig(configProvider, "aws")

	// then - aws plan has "fast" channel
	require.NoError(t, err)
	assert.Equal(t, "fast", awsChannel)

	// when - get channel from default plan
	defaultChannel, err := GetChannelFromPlanConfig(configProvider, DefaultPlanName)

	// then - default plan has "stable" channel
	require.NoError(t, err)
	assert.Equal(t, "stable", defaultChannel)
}

func TestGetChannelFromPlanConfig_FallbackToDefault(t *testing.T) {
	// given
	configProvider := config.NewConfigMapConfigProvider(
		testutil.NewFakeConfigProviderWithData(map[string]map[string]interface{}{
			"default": {
				"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
metadata:
  name: my-kyma
spec:
  channel: stable
  modules:
    - name: btp-operator
`,
			},
		}),
		"test-config-map",
		config.RuntimeConfigurationRequiredFields,
	)

	// when - try to get channel from non-existent plan (should fallback to default)
	channel, err := GetChannelFromPlanConfig(configProvider, "non-existent-plan")

	// then - falls back to default plan's channel
	require.NoError(t, err)
	assert.Equal(t, "stable", channel)
}

func TestGetChannelFromPlanConfig_NoDefaultPlan(t *testing.T) {
	// given
	configMapProvider := config.NewConfigMapConfigProvider(
		testutil.NewFakeConfigProviderWithData(map[string]map[string]interface{}{
			"aws": {
				"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
metadata:
  name: my-kyma
spec:
  channel: fast
  modules:
    - name: btp-operator
`,
			},
		}),
		"test",
		config.RuntimeConfigurationRequiredFields,
	)

	// when - try to get channel from non-existent plan with no default fallback
	_, err := GetChannelFromPlanConfig(configMapProvider, "non-existent-plan")

	// then - should return error
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to provide configuration")
}

func TestGetChannelFromConfig_WithFetcherFunction(t *testing.T) {
	// given
	kymaTemplate := `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
metadata:
  name: my-kyma
  namespace: kyma-system
spec:
  channel: regular
  modules:
    - name: btp-operator
`
	configFetcher := func() (string, error) {
		return kymaTemplate, nil
	}

	// when
	channel, err := GetChannelFromConfig(configFetcher)

	// then
	require.NoError(t, err)
	assert.Equal(t, "regular", channel)
}

func TestGetChannelFromConfig_EmptyTemplate(t *testing.T) {
	// given
	configFetcher := func() (string, error) {
		return "", nil
	}

	// when
	_, err := GetChannelFromConfig(configFetcher)

	// then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kyma-template is empty")
}

func TestGetChannelFromConfig_InvalidYAML(t *testing.T) {
	// given
	configFetcher := func() (string, error) {
		return "invalid: yaml: [", nil
	}

	// when
	_, err := GetChannelFromConfig(configFetcher)

	// then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to decode kyma template")
}

func TestGetChannelFromConfig_MissingChannel(t *testing.T) {
	// given
	kymaTemplate := `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
metadata:
  name: my-kyma
  namespace: kyma-system
spec:
  modules:
    - name: btp-operator
`
	configFetcher := func() (string, error) {
		return kymaTemplate, nil
	}

	// when
	_, err := GetChannelFromConfig(configFetcher)

	// then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "channel not found in kyma template")
}
