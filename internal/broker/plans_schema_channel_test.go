package broker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewModulesSchema_DefaultChannel(t *testing.T) {
	tests := []struct {
		name            string
		defaultChannel  []string
		expectedDefault string
	}{
		{
			name:            "No default channel provided",
			defaultChannel:  []string{},
			expectedDefault: "regular",
		},
		{
			name:            "Empty default channel provided",
			defaultChannel:  []string{""},
			expectedDefault: "regular",
		},
		{
			name:            "Fast channel provided",
			defaultChannel:  []string{"fast"},
			expectedDefault: "fast",
		},
		{
			name:            "Regular channel provided",
			defaultChannel:  []string{"regular"},
			expectedDefault: "regular",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modules := NewModulesSchema(false, tt.defaultChannel...)

			// Navigate to the default modules schema to get the channel default
			defaultModule := modules.OneOf[0].(ModulesDefault)
			channelDefault := defaultModule.Properties.Channel.Default

			assert.Equal(t, tt.expectedDefault, channelDefault, "Channel default should match expected value")
		})
	}
}

func TestNewProvisioningProperties_DefaultChannel(t *testing.T) {
	machineTypesDisplay := map[string]string{"m5.large": "M5 Large"}
	regionsDisplay := map[string]string{"us-east-1": "US East 1"}
	machineTypes := []string{"m5.large"}
	regions := []string{"us-east-1"}

	tests := []struct {
		name            string
		defaultChannel  []string
		expectedDefault string
	}{
		{
			name:            "No default channel provided",
			defaultChannel:  []string{},
			expectedDefault: "regular",
		},
		{
			name:            "Fast channel provided",
			defaultChannel:  []string{"fast"},
			expectedDefault: "fast",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			properties := NewProvisioningProperties(
				machineTypesDisplay,
				machineTypesDisplay,
				regionsDisplay,
				machineTypes,
				machineTypes,
				regions,
				false,
				false,
				tt.defaultChannel...,
			)

			// Navigate to the modules schema to get the channel default
			defaultModule := properties.Modules.OneOf[0].(ModulesDefault)
			channelDefault := defaultModule.Properties.Channel.Default

			assert.Equal(t, tt.expectedDefault, channelDefault, "Channel default should match expected value")
		})
	}
}
