package main

import (
	"context"
	"fmt"
	"log/slog"

	azurecloud "github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/kyma-project/kyma-environment-broker/common/gardener"
	"github.com/kyma-project/kyma-environment-broker/common/hyperscaler/rules"
	azurehyperscaler "github.com/kyma-project/kyma-environment-broker/internal/hyperscalers/azure"
	"github.com/kyma-project/kyma-environment-broker/internal/provider/configuration"
	"github.com/kyma-project/kyma-environment-broker/internal/subscriptions"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// buildAzureSecretFetcher returns a SecretFetcher backed by a BindingProvider that caches
// the last known-good CredentialsBinding. On each cache refresh the cached binding is
// re-validated; if it fails a new one is searched via FindValidBinding.
func buildAzureSecretFetcher(gardenerClient *gardener.Client, rulesService *rules.RulesService, log *slog.Logger) (azurehyperscaler.SecretFetcher, error) {
	items, err := listAzureBindingItems(gardenerClient, rulesService)
	if err != nil {
		return nil, err
	}

	provider := gardener.NewBindingProvider(items, log)
	return func() (azurehyperscaler.AzureCredentials, error) {
		// Use a fresh background context — the startup ctx would be stale by the time
		// this fetcher is called during hourly cache refresh.
		v := &azurehyperscaler.AzureBindingValidator{GardenerClient: gardenerClient}
		_, err := provider.Get(context.Background(), v)
		if err != nil {
			return azurehyperscaler.AzureCredentials{}, err
		}
		return v.DiscoveredCreds, nil
	}, nil
}

// resolveAzureCloudConfig determines the Azure cloud environment for zone discovery API calls.
// When clientConfiguration is set explicitly, it is used directly with no network calls.
// Otherwise the cloud is auto-discovered by probing Public → China → US Gov at startup.
func resolveAzureCloudConfig(ctx context.Context, providerSpec *configuration.ProviderSpec, gardenerClient *gardener.Client, rulesService *rules.RulesService, log *slog.Logger) (azurecloud.Configuration, error) {
	if configName := providerSpec.AzureClientConfiguration(); configName != "" {
		cfg, err := azurehyperscaler.CloudConfigFromName(configName)
		if err != nil {
			return azurecloud.Configuration{}, err
		}
		log.Info("Azure cloud configured explicitly", "cloud", configName)
		return cfg, nil
	}

	items, err := listAzureBindingItems(gardenerClient, rulesService)
	if err != nil {
		return azurecloud.Configuration{}, fmt.Errorf("Azure cloud auto-discovery not possible: %w", err)
	}

	v := &azurehyperscaler.AzureBindingValidator{GardenerClient: gardenerClient}
	_, err = gardener.FindValidBinding(ctx, items, log, v)
	if err != nil {
		return azurecloud.Configuration{}, fmt.Errorf("Azure cloud auto-discovery failed: %w", err)
	}
	return v.DiscoveredCloud, nil
}

func listAzureBindingItems(gardenerClient *gardener.Client, rulesService *rules.RulesService) ([]unstructured.Unstructured, error) {
	attr := &rules.ProvisioningAttributes{
		Plan:        "azure",
		Hyperscaler: "azure",
	}
	matchedRule, found := rulesService.MatchProvisioningAttributesWithValidRuleset(attr)
	if !found {
		return nil, fmt.Errorf("no matching rule for azure hyperscaler")
	}
	labelSelector := subscriptions.NewLabelSelectorFromRuleset(matchedRule).BuildAnySubscription()

	credentialsBindings, err := gardenerClient.GetCredentialsBindings(labelSelector)
	if err != nil {
		return nil, fmt.Errorf("while getting Azure credentials bindings: %w", err)
	}
	if credentialsBindings == nil || len(credentialsBindings.Items) == 0 {
		return nil, fmt.Errorf("no Azure credentials bindings found for selector %q", labelSelector)
	}
	return credentialsBindings.Items, nil
}
