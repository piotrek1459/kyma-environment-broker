package broker

import (
	"bytes"
	"fmt"

	"github.com/kyma-project/kyma-environment-broker/internal/config"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8syaml "k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	k8syamlutil "k8s.io/apimachinery/pkg/util/yaml"
)

// GetChannelFromConfig reads the channel from the default Kyma template configuration
func GetChannelFromConfig(configProvider config.ConfigMapConfigProvider) (string, error) {
	cfg := make(map[string]interface{})
	err := configProvider.Provide("default", &cfg)
	if err != nil {
		return "", fmt.Errorf("unable to provide default configuration: %w", err)
	}

	kymaTemplateRaw, exists := cfg["kyma-template"]
	if !exists {
		return "", fmt.Errorf("kyma-template not found in default configuration")
	}

	kymaTemplate, ok := kymaTemplateRaw.(string)
	if !ok {
		return "", fmt.Errorf("kyma-template is not a string in default configuration")
	}

	if kymaTemplate == "" {
		return "", fmt.Errorf("kyma-template is empty in default configuration")
	}

	obj, err := decodeKymaTemplate(kymaTemplate)
	if err != nil {
		return "", fmt.Errorf("unable to decode kyma template: %w", err)
	}

	channel, found, err := unstructured.NestedString(obj.Object, "spec", "channel")
	if err != nil {
		return "", fmt.Errorf("failed to read channel from kyma template: %w", err)
	}

	if !found {
		return "", fmt.Errorf("channel not found in kyma template")
	}

	return channel, nil
}

func decodeKymaTemplate(kymaTemplate string) (*unstructured.Unstructured, error) {
	tmpl := []byte(kymaTemplate)

	decoder := k8syamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(tmpl), 512)
	var rawObj runtime.RawExtension
	if err := decoder.Decode(&rawObj); err != nil {
		return nil, err
	}
	obj, _, err := k8syaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme).Decode(rawObj.Raw, nil, nil)
	if err != nil {
		return nil, err
	}

	unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	unstructuredObj := &unstructured.Unstructured{Object: unstructuredMap}
	return unstructuredObj, err
}
