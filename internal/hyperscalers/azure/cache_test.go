package azure

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/kyma-project/kyma-environment-broker/internal/provider/configuration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func buildAzureSecret() *unstructured.Unstructured {
	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	s := &unstructured.Unstructured{}
	s.Object = map[string]interface{}{
		"data": map[string]interface{}{
			"clientID":       enc("test-client-id"),
			"clientSecret":   enc("test-client-secret"),
			"tenantID":       enc("test-tenant-id"),
			"subscriptionID": enc("test-subscription-id"),
		},
	}
	return s
}

func buildCacheSpec(machineNames []string) *configuration.ProviderSpec {
	machines := ""
	for _, name := range machineNames {
		machines += "    \"" + name + "\": \"" + name + "\"\n"
	}
	yaml := "azure:\n  zonesDiscovery: true\n  regions:\n    westeurope:\n      displayName: \"West Europe\"\n  machines:\n" + machines
	spec, err := configuration.NewProviderSpec(strings.NewReader(yaml))
	if err != nil {
		panic(fmt.Sprintf("buildCacheSpec: failed to parse spec: %v", err))
	}
	return spec
}

// mockSKUsClientFactory returns a SKUsClientFactory that always returns the given API.
func mockSKUsClientFactory(api ResourceSKUsAPI) SKUsClientFactory {
	return func(_ string, _ *azidentity.ClientSecretCredential) (ResourceSKUsAPI, error) {
		return api, nil
	}
}

func newTestCache(spec *configuration.ProviderSpec, skus []*armcompute.ResourceSKU, apiErr error) *AzureCache {
	return &AzureCache{
		data:              make(map[string]map[string][]string),
		providerSpec:      spec,
		secretFetcher:     func() (*unstructured.Unstructured, error) { return buildAzureSecret(), nil },
		skusClientFactory: mockSKUsClientFactory(&mockSKUsAPI{skus: skus, err: apiErr}),
	}
}

func TestAzureCache_FillAndRead(t *testing.T) {
	skus := []*armcompute.ResourceSKU{
		buildSKU("Standard_D4s_v5", []string{"1", "2", "3"}, nil),
		buildSKU("Standard_F8s_v2", []string{"1", "2"}, nil),
	}
	spec := buildCacheSpec([]string{"Standard_D4s_v5", "Standard_F8s_v2"})
	cache := newTestCache(spec, skus, nil)

	err := cache.fillRegion(context.Background(), buildAzureSecret(), "westeurope")
	require.NoError(t, err)

	assert.True(t, cache.Ready("westeurope"))
	assert.ElementsMatch(t, []string{"1", "2", "3"}, cache.ZonesFor("westeurope", "Standard_D4s_v5"))
	assert.ElementsMatch(t, []string{"1", "2"}, cache.ZonesFor("westeurope", "Standard_F8s_v2"))
	assert.Nil(t, cache.ZonesFor("westeurope", "Standard_Unknown"), "unknown machine type returns nil")
	assert.Nil(t, cache.ZonesFor("eastus", "Standard_D4s_v5"), "unfilled region returns nil")
}

func TestAzureCache_SecretFetcherError(t *testing.T) {
	spec := buildCacheSpec([]string{"Standard_D4s_v5"})
	cache := &AzureCache{
		data:         make(map[string]map[string][]string),
		providerSpec: spec,
		secretFetcher: func() (*unstructured.Unstructured, error) {
			return nil, assert.AnError
		},
		skusClientFactory: defaultSKUsClientFactory,
	}

	cache.fillAll(context.Background())

	assert.False(t, cache.Ready("westeurope"))
	assert.Nil(t, cache.ZonesFor("westeurope", "Standard_D4s_v5"))
}

func TestAzureCache_ContextCancellation(t *testing.T) {
	spec := buildCacheSpec([]string{"Standard_D4s_v5"})
	skus := []*armcompute.ResourceSKU{buildSKU("Standard_D4s_v5", []string{"1", "2", "3"}, nil)}
	cache := newTestCache(spec, skus, nil)

	ctx, cancel := context.WithCancel(context.Background())
	goroutineDone := make(chan struct{})

	go func() {
		cache.run(ctx)
		close(goroutineDone)
	}()

	cancel()

	select {
	case <-goroutineDone:
		// goroutine stopped cleanly
	case <-time.After(3 * time.Second):
		t.Fatal("goroutine did not stop after context cancellation")
	}
}

func TestAzureCache_ConcurrentReads(t *testing.T) {
	// Run with: go test -race ./internal/hyperscalers/azure/...
	skus := []*armcompute.ResourceSKU{
		buildSKU("Standard_D4s_v5", []string{"1", "2", "3"}, nil),
	}
	spec := buildCacheSpec([]string{"Standard_D4s_v5"})
	cache := newTestCache(spec, skus, nil)

	require.NoError(t, cache.fillRegion(context.Background(), buildAzureSecret(), "westeurope"))

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			zones := cache.ZonesFor("westeurope", "Standard_D4s_v5")
			assert.NotNil(t, zones)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = cache.fillRegion(context.Background(), buildAzureSecret(), "westeurope")
	}()
	wg.Wait()
}

func TestAzureCache_RetryOnFillRegionError(t *testing.T) {
	// AzureClient.fillCache retries 'retries' times on API error.
	spec := buildCacheSpec([]string{"Standard_D4s_v5"})
	callCount := 0

	client := &AzureClient{
		skusClient: &countingMockSKUsAPI{
			skus:        nil,
			callCounter: &callCount,
			err:         assert.AnError,
		},
		region:       "westeurope",
		providerSpec: spec,
	}

	err := client.fillCache(context.Background())
	require.Error(t, err)
	assert.Equal(t, retries, callCount)
}

func TestAzureCachedClient_ReturnsFromCache(t *testing.T) {
	skus := []*armcompute.ResourceSKU{
		buildSKU("Standard_D4s_v5", []string{"1", "2", "3"}, nil),
	}
	spec := buildCacheSpec([]string{"Standard_D4s_v5"})
	cache := newTestCache(spec, skus, nil)

	require.NoError(t, cache.fillRegion(context.Background(), buildAzureSecret(), "westeurope"))

	client := NewCachedClient(cache, "westeurope", spec)

	zones, err := client.AvailableZones(context.Background(), "Standard_D4s_v5")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"1", "2", "3"}, zones)

	count, err := client.AvailableZonesCount(context.Background(), "Standard_D4s_v5")
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

func TestAzureCachedClient_NotReadyRegionReturnsNil(t *testing.T) {
	spec := buildCacheSpec([]string{"Standard_D4s_v5"})
	cache := &AzureCache{
		data:         make(map[string]map[string][]string),
		providerSpec: spec,
	}

	client := NewCachedClient(cache, "westeurope", spec)
	zones, err := client.AvailableZones(context.Background(), "Standard_D4s_v5")
	require.NoError(t, err)
	assert.Nil(t, zones, "nil zones when cache not ready — caller uses fallback per-call client")
}
