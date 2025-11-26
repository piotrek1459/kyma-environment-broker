package broker

import (
	"testing"

	pkg "github.com/kyma-project/kyma-environment-broker/common/runtime"
	"github.com/kyma-project/kyma-environment-broker/internal/broker/testutil"
	"github.com/kyma-project/kyma-environment-broker/internal/config"
	"github.com/kyma-project/kyma-environment-broker/internal/provider/configuration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchemaService_PlanAwareChannels(t *testing.T) {
	// Given: Different channels configured for different plans
	fakeProvider := testutil.NewFakeConfigProviderWithData(map[string]map[string]interface{}{
		"default": {
			"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
spec:
  channel: regular
  modules: []`,
		},
		"aws": {
			"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
spec:
  channel: fast
  modules: []`,
		},
		"azure": {
			"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
spec:
  channel: regular
  modules: []`,
		},
		"gcp": {
			"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
spec:
  channel: fast
  modules: []`,
		},
	})

	configProvider := config.NewConfigMapConfigProvider(fakeProvider, "test-config-map", config.RuntimeConfigurationRequiredFields)

	plans, err := configuration.NewPlanSpecificationsFromFile("testdata/plans.yaml")
	require.NoError(t, err)
	provider, err := configuration.NewProviderSpecFromFile("testdata/providers.yaml")
	require.NoError(t, err)

	schemaService := NewSchemaService(provider, plans, nil, Config{
		RejectUnsupportedParameters: true,
		DualStackDocsURL:            "https://placeholder.com",
	}, EnablePlans{AWSPlanName, AzurePlanName, GCPPlanName}, configProvider)

	// When: Generate schemas for different plans
	awsCreateSchema, _, _ := schemaService.AWSSchemas("cf-eu11")
	azureCreateSchema, _, _ := schemaService.AzureSchemas("cf-us21")
	gcpCreateSchema, _, _ := schemaService.GCPSchemas("cf-us11")

	// Then: Each plan should have its configured channel
	awsChannel := extractChannelFromSchema(t, awsCreateSchema)
	assert.Equal(t, "fast", awsChannel, "AWS plan should use 'fast' channel")

	azureChannel := extractChannelFromSchema(t, azureCreateSchema)
	assert.Equal(t, "regular", azureChannel, "Azure plan should use 'regular' channel")

	gcpChannel := extractChannelFromSchema(t, gcpCreateSchema)
	assert.Equal(t, "fast", gcpChannel, "GCP plan should use 'fast' channel")
}

func TestSchemaService_PlanAwareChannels_FallbackToDefault(t *testing.T) {
	// Given: Only default channel configured, no plan-specific configs
	fakeProvider := testutil.NewFakeConfigProviderWithData(map[string]map[string]interface{}{
		"default": {
			"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
spec:
  channel: regular
  modules: []`,
		},
		// No plan-specific configs - should fallback to default
	})

	configProvider := config.NewConfigMapConfigProvider(fakeProvider, "test-config-map", config.RuntimeConfigurationRequiredFields)

	plans, err := configuration.NewPlanSpecificationsFromFile("testdata/plans.yaml")
	require.NoError(t, err)
	provider, err := configuration.NewProviderSpecFromFile("testdata/providers.yaml")
	require.NoError(t, err)

	schemaService := NewSchemaService(provider, plans, nil, Config{
		RejectUnsupportedParameters: true,
		DualStackDocsURL:            "https://placeholder.com",
	}, EnablePlans{AWSPlanName, AzurePlanName, GCPPlanName}, configProvider)

	// When: Generate schemas for plans without specific config
	awsCreateSchema, _, _ := schemaService.AWSSchemas("cf-eu11")
	azureCreateSchema, _, _ := schemaService.AzureSchemas("cf-us21")
	gcpCreateSchema, _, _ := schemaService.GCPSchemas("cf-us11")

	// Then: All plans should fallback to default channel
	awsChannel := extractChannelFromSchema(t, awsCreateSchema)
	assert.Equal(t, "regular", awsChannel, "AWS plan should fallback to default 'regular' channel")

	azureChannel := extractChannelFromSchema(t, azureCreateSchema)
	assert.Equal(t, "regular", azureChannel, "Azure plan should fallback to default 'regular' channel")

	gcpChannel := extractChannelFromSchema(t, gcpCreateSchema)
	assert.Equal(t, "regular", gcpChannel, "GCP plan should fallback to default 'regular' channel")
}

func TestSchemaService_SpecialPlans_PlanAwareChannels(t *testing.T) {
	// Given: Different channels for special plans (Trial, Free, OwnCluster, AzureLite)
	fakeProvider := testutil.NewFakeConfigProviderWithData(map[string]map[string]interface{}{
		"default": {
			"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
spec:
  channel: regular
  modules: []`,
		},
		"trial": {
			"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
spec:
  channel: fast
  modules: []`,
		},
		"free": {
			"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
spec:
  channel: fast
  modules: []`,
		},
		"own_cluster": {
			"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
spec:
  channel: regular
  modules: []`,
		},
		"azure_lite": {
			"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
spec:
  channel: fast
  modules: []`,
		},
	})

	configProvider := config.NewConfigMapConfigProvider(fakeProvider, "test-config-map", config.RuntimeConfigurationRequiredFields)

	plans, err := configuration.NewPlanSpecificationsFromFile("testdata/plans.yaml")
	require.NoError(t, err)
	provider, err := configuration.NewProviderSpecFromFile("testdata/providers.yaml")
	require.NoError(t, err)

	schemaService := NewSchemaService(provider, plans, nil, Config{
		RejectUnsupportedParameters: true,
		DualStackDocsURL:            "https://placeholder.com",
	}, EnablePlans{TrialPlanName, FreemiumPlanName, OwnClusterPlanName, AzureLitePlanName}, configProvider)

	// When: Generate schemas for special plans
	trialSchema := schemaService.TrialSchema(false)
	freeSchema := schemaService.FreeSchema(pkg.AWS, "cf-us21", false)
	ownClusterSchema := schemaService.OwnClusterSchema(false)
	azureLiteSchema, _, _ := schemaService.AzureLiteSchemas("cf-us21")

	// Then: Each plan should have its configured channel
	trialChannel := extractChannelFromSchema(t, trialSchema)
	assert.Equal(t, "fast", trialChannel, "Trial plan should use 'fast' channel")

	freeChannel := extractChannelFromSchema(t, freeSchema)
	assert.Equal(t, "fast", freeChannel, "Free plan should use 'fast' channel")

	ownClusterChannel := extractChannelFromSchema(t, ownClusterSchema)
	assert.Equal(t, "regular", ownClusterChannel, "OwnCluster plan should use 'regular' channel")

	azureLiteChannel := extractChannelFromSchema(t, azureLiteSchema)
	assert.Equal(t, "fast", azureLiteChannel, "AzureLite plan should use 'fast' channel")
}

func TestSchemaService_ComputePlanChannels(t *testing.T) {
	// Given: Config provider with various plan configurations
	fakeProvider := testutil.NewFakeConfigProviderWithData(map[string]map[string]interface{}{
		"default": {
			"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
spec:
  channel: regular
  modules: []`,
		},
		"aws": {
			"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
spec:
  channel: fast
  modules: []`,
		},
		"azure": {
			"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
spec:
  channel: fast
  modules: []`,
		},
		// GCP not configured - should fallback to default
	})

	configProvider := config.NewConfigMapConfigProvider(fakeProvider, "test-config-map", config.RuntimeConfigurationRequiredFields)

	// When: Compute plan channels
	planChannels := computePlanChannels(configProvider)

	// Then: Verify computed channels
	assert.Equal(t, "fast", planChannels[AWSPlanName], "AWS should have 'fast' channel")
	assert.Equal(t, "fast", planChannels[AzurePlanName], "Azure should have 'fast' channel")
	assert.Equal(t, "regular", planChannels[GCPPlanName], "GCP should fallback to 'regular' channel")

	// Verify all known plans have a channel (DefaultPlanName is not a real plan, it's the fallback key)
	knownPlans := []string{
		AWSPlanName, AzurePlanName, GCPPlanName, AzureLitePlanName,
		FreemiumPlanName, TrialPlanName, OwnClusterPlanName,
		PreviewPlanName, BuildRuntimeAWSPlanName, BuildRuntimeGCPPlanName,
		BuildRuntimeAzurePlanName, SapConvergedCloudPlanName, AlicloudPlanName,
	}

	for _, planName := range knownPlans {
		channel, exists := planChannels[planName]
		assert.True(t, exists, "Plan %s should have a channel", planName)
		assert.NotEmpty(t, channel, "Plan %s channel should not be empty", planName)
	}
}

func TestSchemaService_GetChannelForPlan(t *testing.T) {
	// Given
	fakeProvider := testutil.NewFakeConfigProviderWithData(map[string]map[string]interface{}{
		"default": {
			"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
spec:
  channel: regular
  modules: []`,
		},
		"aws": {
			"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
spec:
  channel: fast
  modules: []`,
		},
	})

	configProvider := config.NewConfigMapConfigProvider(fakeProvider, "test-config-map", config.RuntimeConfigurationRequiredFields)
	plans, err := configuration.NewPlanSpecificationsFromFile("testdata/plans.yaml")
	require.NoError(t, err)
	provider, err := configuration.NewProviderSpecFromFile("testdata/providers.yaml")
	require.NoError(t, err)

	schemaService := NewSchemaService(provider, plans, nil, Config{}, EnablePlans{}, configProvider)

	// When & Then: Test getChannelForPlan method
	assert.Equal(t, "fast", schemaService.getChannelForPlan(AWSPlanName), "AWS plan should return 'fast'")
	assert.Equal(t, "regular", schemaService.getChannelForPlan(GCPPlanName), "GCP plan should fallback to 'regular'")
	assert.Equal(t, "regular", schemaService.getChannelForPlan("non-existent-plan"), "Unknown plan should fallback to hard-coded 'regular'")
}

// Helper function to extract channel from schema JSON
func extractChannelFromSchema(t *testing.T, schema *map[string]interface{}) string {
	require.NotNil(t, schema, "Schema should not be nil")

	schemaMap := *schema

	properties, ok := schemaMap["properties"].(map[string]interface{})
	require.True(t, ok, "Schema should have properties")

	modules, ok := properties["modules"].(map[string]interface{})
	if !ok {
		// Some schemas might not have modules field
		return ""
	}

	// The modules field has a "oneOf" structure
	oneOf, ok := modules["oneOf"].([]interface{})
	if !ok {
		return ""
	}

	if len(oneOf) == 0 {
		return ""
	}

	// First item in oneOf is the "default modules" option
	defaultOption, ok := oneOf[0].(map[string]interface{})
	if !ok {
		return ""
	}

	optionProps, ok := defaultOption["properties"].(map[string]interface{})
	if !ok {
		return ""
	}

	channelProp, ok := optionProps["channel"].(map[string]interface{})
	if !ok {
		return ""
	}

	defaultValue, ok := channelProp["default"].(string)
	if !ok {
		return ""
	}

	return defaultValue
}
