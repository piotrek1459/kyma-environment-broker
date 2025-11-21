package testutil

import (
	"fmt"

	"github.com/kyma-project/kyma-environment-broker/internal/config"
)

// FakeProvider implements config.Provider for testing
type FakeProvider struct {
	// Data holds configuration per plan. If nil, uses default config.
	Data map[string]map[string]interface{}
}

func (f *FakeProvider) Provide(cfgSrcName, cfgKeyName, reqCfgKeys string, cfgDestObj any) error {
	configMap, ok := cfgDestObj.(*map[string]interface{})
	if !ok {
		return fmt.Errorf("target must be a pointer to map[string]interface{}")
	}

	// If Data is configured, use it
	if f.Data != nil {
		data, exists := f.Data[cfgKeyName]
		if !exists {
			return fmt.Errorf("plan %s not found", cfgKeyName)
		}
		*configMap = data
		return nil
	}

	// Default behavior for backward compatibility
	defaultConfig := map[string]interface{}{
		"kyma-template": `apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
metadata:
  name: default
spec:
  channel: regular
  modules:
    - name: btp-operator
      channel: regular
    - name: keda
      channel: fast
`,
	}

	if cfgKeyName == "default" {
		*configMap = defaultConfig
	} else {
		*configMap = make(map[string]interface{})
	}
	return nil
}

// NewFakeConfigProvider returns a new instance of the fake config provider with default behavior
func NewFakeConfigProvider() config.Provider {
	return &FakeProvider{}
}

// NewFakeConfigProviderWithData returns a fake config provider with custom data per plan
func NewFakeConfigProviderWithData(data map[string]map[string]interface{}) config.Provider {
	return &FakeProvider{Data: data}
}
