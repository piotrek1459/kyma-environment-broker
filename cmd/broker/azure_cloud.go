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
)

// buildAzureSecretFetcher returns a SecretFetcher that fetches and validates Azure
// credentials on each call. Validation uses a token probe against the known cloudConfig,
// ensuring that a binding with stale credentials is skipped via TryWithBindings retry.
func buildAzureSecretFetcher(ctx context.Context, gardenerClient *gardener.Client, rulesService *rules.RulesService, cloudConfig azurecloud.Configuration, log *slog.Logger) (azurehyperscaler.SecretFetcher, error) {
	attr := &rules.ProvisioningAttributes{
		Plan:        "azure",
		Hyperscaler: "azure",
	}
	matchedRule, found := rulesService.MatchProvisioningAttributesWithValidRuleset(attr)
	if !found {
		return nil, fmt.Errorf("no matching rule for azure hyperscaler")
	}
	labelSelector := subscriptions.NewLabelSelectorFromRuleset(matchedRule).BuildAnySubscription()

	return func() (azurehyperscaler.AzureCredentials, error) {
		return fetchAzureCredentials(ctx, gardenerClient, labelSelector, cloudConfig, log)
	}, nil
}

func fetchAzureCredentials(ctx context.Context, gardenerClient *gardener.Client, labelSelector string, cloudConfig azurecloud.Configuration, log *slog.Logger) (azurehyperscaler.AzureCredentials, error) {
	credentialsBindings, err := gardenerClient.GetCredentialsBindings(labelSelector)
	if err != nil {
		return azurehyperscaler.AzureCredentials{}, fmt.Errorf("while getting Azure credentials bindings: %w", err)
	}
	if credentialsBindings == nil || len(credentialsBindings.Items) == 0 {
		return azurehyperscaler.AzureCredentials{}, fmt.Errorf("no Azure credentials bindings found for selector %q", labelSelector)
	}
	return gardener.TryWithBindings(credentialsBindings.Items, log, func(cb *gardener.CredentialsBinding) (azurehyperscaler.AzureCredentials, error) {
		log.Info("fetching Azure credentials", "credentialBinding", cb.GetName())
		secret, err := gardenerClient.GetSecret(cb.GetSecretRefNamespace(), cb.GetSecretRefName())
		if err != nil {
			return azurehyperscaler.AzureCredentials{}, fmt.Errorf("unable to get Azure secret %s/%s: %w", cb.GetSecretRefNamespace(), cb.GetSecretRefName(), err)
		}
		creds, err := azurehyperscaler.ExtractCredentials(secret)
		if err != nil {
			return azurehyperscaler.AzureCredentials{}, err
		}
		if err := azurehyperscaler.ValidateCredentials(ctx, creds, cloudConfig); err != nil {
			return azurehyperscaler.AzureCredentials{}, fmt.Errorf("credentials validation failed for binding %s: %w", cb.GetName(), err)
		}
		return creds, nil
	})
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

	attr := &rules.ProvisioningAttributes{
		Plan:        "azure",
		Hyperscaler: "azure",
	}
	matchedRule, found := rulesService.MatchProvisioningAttributesWithValidRuleset(attr)
	if !found {
		return azurecloud.Configuration{}, fmt.Errorf("Azure cloud auto-discovery not possible: no matching rule for azure hyperscaler")
	}
	labelSelector := subscriptions.NewLabelSelectorFromRuleset(matchedRule).BuildAnySubscription()

	credentialsBindings, err := gardenerClient.GetCredentialsBindings(labelSelector)
	if err != nil {
		return azurecloud.Configuration{}, fmt.Errorf("while getting Azure credentials bindings: %w", err)
	}
	if credentialsBindings == nil || len(credentialsBindings.Items) == 0 {
		return azurecloud.Configuration{}, fmt.Errorf("no Azure credentials bindings found for selector %q", labelSelector)
	}

	return gardener.TryWithBindings(credentialsBindings.Items, log, func(cb *gardener.CredentialsBinding) (azurecloud.Configuration, error) {
		log.Info("trying Azure credentials for cloud discovery", "credentialBinding", cb.GetName())
		secret, err := gardenerClient.GetSecret(cb.GetSecretRefNamespace(), cb.GetSecretRefName())
		if err != nil {
			return azurecloud.Configuration{}, fmt.Errorf("unable to get Azure secret %s/%s: %w", cb.GetSecretRefNamespace(), cb.GetSecretRefName(), err)
		}
		creds, err := azurehyperscaler.ExtractCredentials(secret)
		if err != nil {
			return azurecloud.Configuration{}, err
		}
		return azurehyperscaler.ResolveCloudConfig(ctx, creds)
	})
}
