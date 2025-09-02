// build provisioning-test
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/kyma-project/kyma-environment-broker/internal/provider"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/google/uuid"
	"github.com/pivotal-cf/brokerapi/v12/domain"
	"github.com/stretchr/testify/assert"

	pkg "github.com/kyma-project/kyma-environment-broker/common/runtime"
	"github.com/kyma-project/kyma-environment-broker/internal"
	"github.com/kyma-project/kyma-environment-broker/internal/broker"
	"github.com/kyma-project/kyma-environment-broker/internal/ptr"
)

const (
	workersAmount                 int = 2
	provisioningRequestPathFormat     = "oauth/cf-eu10/v2/service_instances/%s"
)

func TestCatalog(t *testing.T) {
	// this test is used for human-testing the catalog response
	//t.Skip()
	catalogTestFile := "catalog-test.json"
	catalogTestFilePerm := os.FileMode.Perm(0666)
	outputToFile := true
	prettyJson := true
	prettify := func(content []byte) *bytes.Buffer {
		var prettyJSON bytes.Buffer
		err := json.Indent(&prettyJSON, content, "", "    ")
		assert.NoError(t, err)
		return &prettyJSON
	}

	// given
	suite := NewBrokerSuiteTest(t)
	defer suite.TearDown()

	// when
	resp := suite.CallAPI("GET", fmt.Sprintf("oauth/v2/catalog"), ``)

	content, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	defer resp.Body.Close()

	if outputToFile {
		if prettyJson {
			err = os.WriteFile(catalogTestFile, prettify(content).Bytes(), catalogTestFilePerm)
			assert.NoError(t, err)
		} else {
			err = os.WriteFile(catalogTestFile, content, catalogTestFilePerm)
			assert.NoError(t, err)
		}
	} else {
		if prettyJson {
			fmt.Println(prettify(content).String())
		} else {
			fmt.Println(string(content))
		}
	}
}

func TestProvisioningForTrial(t *testing.T) {

	cfg := fixConfig()
	suite := NewBrokerSuiteTestWithConfig(t, cfg)
	defer suite.TearDown()
	iid := uuid.New().String()
	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/cf-eu21/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "7d55d31d-35ae-4438-bf13-6ffdfa107d9f",
					"context": {
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"administrators":["newAdmin1@kyma.cx", "newAdmin2@kyma.cx"],
						"machineType": "unsupported-machine-type",
						"autoscalerMax": 13,
						"autoscalerMin": 13
					}
		}`)

	opID := suite.DecodeOperationID(resp)

	suite.processKIMProvisioningByOperationID(opID)

	// then
	suite.WaitForOperationState(opID, domain.Succeeded)
	suite.AssertRuntimeResourceLabels(opID)

	runtimeResource := suite.GetUnstructuredRuntimeResource(opID)
	suite.AssertRuntimeResourceWorkers(runtimeResource, "m5.xlarge", 1, 1)

	op, err := suite.db.Operations().GetOperationByID(opID)
	require.NoError(t, err)
	assert.Equal(t, "eu-west-1", op.Region)
	assert.Equal(t, "g-account-id", op.GlobalAccountID)
}

func TestProvisioningForAWS(t *testing.T) {

	cfg := fixConfig()

	suite := NewBrokerSuiteTestWithConfig(t, cfg)
	defer suite.TearDown()
	iid := uuid.New().String()
	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/cf-eu21/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "eu-central-1",
						"administrators":["newAdmin1@kyma.cx", "newAdmin2@kyma.cx"]
					}
		}`)

	opID := suite.DecodeOperationID(resp)

	suite.processKIMProvisioningByOperationID(opID)

	// then
	suite.WaitForOperationState(opID, domain.Succeeded)
	suite.AssertRuntimeResourceLabels(opID)
}

func TestProvisioning_HappyPathAWS(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t)
	defer suite.TearDown()
	iid := uuid.New().String()

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "eu-central-1"
					}
		}`)
	opID := suite.DecodeOperationID(resp)

	suite.processKIMProvisioningByOperationID(opID)

	// then
	suite.WaitForOperationState(opID, domain.Succeeded)

	suite.AssertKymaResourceExists(opID)
	suite.AssertKymaLabelsExist(opID, map[string]string{"kyma-project.io/region": "eu-central-1", "kyma-project.io/provider": "AWS"})
	suite.AssertKymaLabelNotExists(opID, "kyma-project.io/platform-region")
}

func TestProvisioning_ColocateControlPlane(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t)
	defer suite.TearDown()

	t.Run("should accept the provisioning param and set EnforceSeedLocation to false in Runtime CR", func(t *testing.T) {
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "us-west-2",
						"colocateControlPlane": false
					}
		}`)
		opID := suite.DecodeOperationID(resp)

		suite.processKIMProvisioningByOperationID(opID)

		// then
		suite.WaitForOperationState(opID, domain.Succeeded)

		runtime := suite.GetRuntimeResourceByInstanceID(iid)
		assert.False(t, *runtime.Spec.Shoot.EnforceSeedLocation)
	})

	t.Run("should accept the provisioning param and set EnforceSeedLocation to true in Runtime CR", func(t *testing.T) {
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "eu-central-1",
						"colocateControlPlane": true
					}
		}`)
		opID := suite.DecodeOperationID(resp)

		suite.processKIMProvisioningByOperationID(opID)

		// then
		suite.WaitForOperationState(opID, domain.Succeeded)

		runtime := suite.GetRuntimeResourceByInstanceID(iid)
		assert.True(t, *runtime.Spec.Shoot.EnforceSeedLocation)
	})

	t.Run("should return error when seed does not exist in selected region and colocateControlPlane is set to true", func(t *testing.T) {
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "us-east-1",
						"colocateControlPlane": true
					}
		}`)

		parsedResponse := suite.ReadResponse(resp)
		assert.Contains(t, string(parsedResponse), "validation of the region for colocating the control plane: cannot colocate the control plane in the us-east-1 region")
	})
}

func TestProvisioning_IngressFiltering_Enabled(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t)
	defer suite.TearDown()

	t.Run("should accept the ingress filtering param and set Filter.Ingress.Enabled to true in Runtime CR", func(t *testing.T) {
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "eu-central-1",
						"ingressFiltering": true
					}
		}`)
		opID := suite.DecodeOperationID(resp)

		suite.processKIMProvisioningByOperationID(opID)

		// then
		suite.WaitForOperationState(opID, domain.Succeeded)

		runtime := suite.GetRuntimeResourceByInstanceID(iid)
		assert.True(t, runtime.Spec.Security.Networking.Filter.Ingress.Enabled)
	})

	t.Run("should accept the ingress filtering param and set Filter.Ingress.Enabled to false in Runtime CR", func(t *testing.T) {
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "eu-central-1",
						"ingressFiltering": false
					}
		}`)
		opID := suite.DecodeOperationID(resp)

		suite.processKIMProvisioningByOperationID(opID)

		// then
		suite.WaitForOperationState(opID, domain.Succeeded)

		runtime := suite.GetRuntimeResourceByInstanceID(iid)
		assert.False(t, runtime.Spec.Security.Networking.Filter.Ingress.Enabled)
	})

	t.Run("should set Filter.Ingress.Enabled to default false when there is no parameter", func(t *testing.T) {
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "eu-central-1"
					}
		}`)
		opID := suite.DecodeOperationID(resp)

		suite.processKIMProvisioningByOperationID(opID)

		// then
		suite.WaitForOperationState(opID, domain.Succeeded)

		runtime := suite.GetRuntimeResourceByInstanceID(iid)
		assert.False(t, runtime.Spec.Security.Networking.Filter.Ingress.Enabled)
	})

	t.Run("should not accept the enabling ingress filtering for SapConvergedCloud plan", func(t *testing.T) {
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/cf-eu20/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
						"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
						"plan_id": "03b812ac-c991-4528-b5bd-08b303523a63",
						"context": {
							"globalaccount_id": "g-account-id",
							"subaccount_id": "sub-id",
							"user_id": "john.smith@email.com"
						},
						"parameters": {
							"name": "testing-cluster",
							"region": "eu-de-1",
							"ingressFiltering": true
						}
			}`)
		parsedResponse := suite.ReadResponse(resp)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Contains(t, string(parsedResponse), "ingress filtering option is not available")
	})

	t.Run("should not accept the disabling ingress filtering for SapConvergedCloud plan", func(t *testing.T) {
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/cf-eu20/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
						"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
						"plan_id": "03b812ac-c991-4528-b5bd-08b303523a63",
						"context": {
							"globalaccount_id": "g-account-id",
							"subaccount_id": "sub-id",
							"user_id": "john.smith@email.com"
						},
						"parameters": {
							"name": "testing-cluster",
							"region": "eu-de-1",
							"ingressFiltering": true
						}
			}`)
		parsedResponse := suite.ReadResponse(resp)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Contains(t, string(parsedResponse), "ingress filtering option is not available")
	})

}

func TestProvisioning_HappyPathSapConvergedCloud(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t)
	defer suite.TearDown()

	t.Run("should provision SAP Converged Cloud", func(t *testing.T) {
		iid := uuid.New().String()
		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/cf-eu20/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
						"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
						"plan_id": "03b812ac-c991-4528-b5bd-08b303523a63",
						"context": {
							"globalaccount_id": "g-account-id",
							"subaccount_id": "sub-id",
							"user_id": "john.smith@email.com"
						},
						"parameters": {
							"name": "testing-cluster",
							"region": "eu-de-1"
						}
			}`)
		opID := suite.DecodeOperationID(resp)

		suite.processKIMProvisioningByOperationID(opID)

		// then
		suite.WaitForOperationState(opID, domain.Succeeded)

		suite.AssertKymaResourceExists(opID)
		suite.AssertKymaLabelsExist(opID, map[string]string{"kyma-project.io/region": "eu-de-1", "kyma-project.io/provider": "SapConvergedCloud"})
	})

	t.Run("should fail for invalid platform region - invalid platform region", func(t *testing.T) {
		iid := uuid.New().String()
		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/invalid/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "03b812ac-c991-4528-b5bd-08b303523a63",
					"context": {
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "eu-de-1"
					}
		}`)
		parsedResponse := suite.ReadResponse(resp)
		assert.Contains(t, string(parsedResponse), "plan-id not in the catalog")
	})

	t.Run("should fail for invalid platform region - default platform region", func(t *testing.T) {
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "03b812ac-c991-4528-b5bd-08b303523a63",
					"context": {
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "eu-de-1"
					}
		}`)
		parsedResponse := suite.ReadResponse(resp)
		assert.Contains(t, string(parsedResponse), "plan-id not in the catalog")
	})

	t.Run("should fail for invalid platform region - invalid Kyma region", func(t *testing.T) {
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/cf-eu20/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "03b812ac-c991-4528-b5bd-08b303523a63",
					"context": {
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "invalid"
					}
		}`)
		parsedResponse := suite.ReadResponse(resp)
		assert.Contains(t, string(parsedResponse), "while validating input parameters: at '/region': value must be")
	})

}

func TestProvisioning_Preview(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t)
	defer suite.TearDown()
	iid := uuid.New().String()

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "5cb3d976-b85c-42ea-a636-79cadda109a9",
					"context": {
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "eu-central-1"
					}
		}`)
	opID := suite.DecodeOperationID(resp)

	suite.waitForRuntimeAndMakeItReady(opID)

	suite.WaitForOperationState(opID, domain.Succeeded)

	suite.AssertKymaResourceExists(opID)
	suite.AssertKymaLabelsExist(opID, map[string]string{
		"kyma-project.io/region":   "eu-central-1",
		"kyma-project.io/provider": "AWS",
	})
	suite.AssertKymaLabelNotExists(opID, "kyma-project.io/platform-region")
}

func TestProvisioning_NetworkingParametersForAWS(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t)
	defer suite.TearDown()
	iid := uuid.New().String()

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
				"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
				"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
		
				"context": {
					"globalaccount_id": "e449f875-b5b2-4485-b7c0-98725c0571bf",
						"subaccount_id": "test",
					"user_id": "piotr.miskiewicz@sap.com"
					
				},
				"parameters": {
					"name": "test",
					"region": "eu-central-1",
					"networking": {
						"nodes": "192.168.48.0/20"
					}
				}
			}
		}`)
	opID := suite.DecodeOperationID(resp)

	suite.processKIMProvisioningByOperationID(opID)

	suite.WaitForOperationState(opID, domain.Succeeded)
}

func TestProvisioning_AllNetworkingParametersForAWS(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t)
	defer suite.TearDown()
	iid := uuid.New().String()

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
				"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
				"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
		
				"context": {
					"globalaccount_id": "e449f875-b5b2-4485-b7c0-98725c0571bf",
						"subaccount_id": "test",
					"user_id": "piotr.miskiewicz@sap.com"
					
				},
				"parameters": {
					"name": "test",
					"region": "eu-central-1",
					"networking": {
						"nodes": "192.168.48.0/20",
						"pods": "10.104.0.0/24",
						"services": "10.105.0.0/24"
					}
				}
			}
		}`)
	opID := suite.DecodeOperationID(resp)

	suite.processKIMProvisioningByOperationID(opID)

	suite.WaitForOperationState(opID, domain.Succeeded)
}

func TestProvisioning_AWSWithEURestrictedAccessBadRequest(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t)
	defer suite.TearDown()
	iid := uuid.New().String()

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/cf-eu11/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "not-whitelisted-global-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region":"us-west-2"
					}
		}`)
	// then
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestProvisioning_AzureWithEURestrictedAccessBadRequest(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t)
	defer suite.TearDown()
	iid := uuid.New().String()

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/cf-ch20/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "4deee563-e5ec-4731-b9b1-53b42d855f0c",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "not-whitelisted-global-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region":"japaneast"
					}
		}`)
	// then
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestProvisioning_AzureWithEURestrictedAccessHappyFlow(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t)
	defer suite.TearDown()
	iid := uuid.New().String()

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/cf-ch20/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "4deee563-e5ec-4731-b9b1-53b42d855f0c",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "whitelisted-global-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region":"switzerlandnorth"
					}
		}`)
	opID := suite.DecodeOperationID(resp)
	suite.processKIMProvisioningByOperationID(opID)
	suite.WaitForOperationState(opID, domain.Succeeded)

	// then
	runtime := suite.GetRuntimeResourceByInstanceID(iid)
	assert.Equal(t, "switzerlandnorth", runtime.Spec.Shoot.Region)
	suite.AssertKymaLabelsExist(opID, map[string]string{
		"kyma-project.io/region":          "switzerlandnorth",
		"kyma-project.io/provider":        "Azure",
		"kyma-project.io/platform-region": "cf-ch20"})
}

func TestProvisioning_AzureWithEURestrictedAccessDefaultRegion(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t)
	defer suite.TearDown()
	iid := uuid.New().String()

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/cf-ch20/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "4deee563-e5ec-4731-b9b1-53b42d855f0c",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "whitelisted-global-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "switzerlandnorth"
					}
		}`)
	opID := suite.DecodeOperationID(resp)
	suite.processKIMProvisioningByOperationID(opID)
	suite.WaitForOperationState(opID, domain.Succeeded)

	// then
	runtime := suite.GetRuntimeResourceByInstanceID(iid)
	assert.Equal(t, "switzerlandnorth", runtime.Spec.Shoot.Region)
	suite.AssertKymaLabelsExist(opID, map[string]string{
		"kyma-project.io/region":          "switzerlandnorth",
		"kyma-project.io/provider":        "Azure",
		"kyma-project.io/platform-region": "cf-ch20"})
}

func TestProvisioning_AWSWithEURestrictedAccessHappyFlow(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t)
	defer suite.TearDown()
	iid := uuid.New().String()

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/cf-eu11/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "whitelisted-global-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region":"eu-central-1"
					}
		}`)
	opID := suite.DecodeOperationID(resp)
	suite.processKIMProvisioningByOperationID(opID)
	suite.WaitForOperationState(opID, domain.Succeeded)

	// then
	runtime := suite.GetRuntimeResourceByInstanceID(iid)
	assert.Equal(t, "eu-central-1", runtime.Spec.Shoot.Region)
	suite.AssertKymaLabelsExist(opID, map[string]string{
		"kyma-project.io/region":          "eu-central-1",
		"kyma-project.io/provider":        "AWS",
		"kyma-project.io/platform-region": "cf-eu11"})

}

func TestProvisioning_AWSWithEURestrictedAccessDefaultRegion(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t)
	defer suite.TearDown()
	iid := uuid.New().String()

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/cf-eu11/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "whitelisted-global-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "eu-central-1"
					}
		}`)
	opID := suite.DecodeOperationID(resp)
	suite.processKIMProvisioningByOperationID(opID)
	suite.WaitForOperationState(opID, domain.Succeeded)

	// then
	runtime := suite.GetRuntimeResourceByInstanceID(iid)
	assert.Equal(t, "eu-central-1", runtime.Spec.Shoot.Region)
	suite.AssertKymaLabelsExist(opID, map[string]string{
		"kyma-project.io/region":          "eu-central-1",
		"kyma-project.io/provider":        "AWS",
		"kyma-project.io/platform-region": "cf-eu11"})

}

func TestProvisioning_TrialWithEmptyRegion(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t)
	defer suite.TearDown()
	iid := uuid.New().String()

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "7d55d31d-35ae-4438-bf13-6ffdfa107d9f",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region":""
					}
		}`)
	opID := suite.DecodeOperationID(resp)
	suite.processKIMProvisioningByOperationID(opID)
	suite.WaitForOperationState(opID, domain.Succeeded)

	// then
	runtime := suite.GetRuntimeResourceByInstanceID(iid)
	assert.Equal(t, "eu-west-1", runtime.Spec.Shoot.Region)
	suite.AssertKymaLabelsExist(opID, map[string]string{
		"kyma-project.io/provider": "AWS",
		"kyma-project.io/region":   "eu-west-1"})
	suite.AssertKymaLabelNotExists(opID, "kyma-project.io/platform-region")

}

func TestProvisioning_Conflict(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t)
	defer suite.TearDown()
	iid := uuid.New().String()

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "7d55d31d-35ae-4438-bf13-6ffdfa107d9f",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster"
					}
		}`)
	opID := suite.DecodeOperationID(resp)
	suite.processKIMProvisioningByOperationID(opID)
	suite.WaitForOperationState(opID, domain.Succeeded)

	// when
	resp = suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "7d55d31d-35ae-4438-bf13-6ffdfa107d9f",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster-2"
					}
		}`)
	// then
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestProvisioning_OwnCluster(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t)
	defer suite.TearDown()
	iid := uuid.New().String()

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "03e3cb66-a4c6-4c6a-b4b0-5d42224debea",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"kubeconfig":"YXBpVmVyc2lvbjogdjEKa2luZDogQ29uZmlnCmN1cnJlbnQtY29udGV4dDogc2hvb3QtLWt5bWEtZGV2LS1jbHVzdGVyLW5hbWUKY29udGV4dHM6CiAgLSBuYW1lOiBzaG9vdC0ta3ltYS1kZXYtLWNsdXN0ZXItbmFtZQogICAgY29udGV4dDoKICAgICAgY2x1c3Rlcjogc2hvb3QtLWt5bWEtZGV2LS1jbHVzdGVyLW5hbWUKICAgICAgdXNlcjogc2hvb3QtLWt5bWEtZGV2LS1jbHVzdGVyLW5hbWUtdG9rZW4KY2x1c3RlcnM6CiAgLSBuYW1lOiBzaG9vdC0ta3ltYS1kZXYtLWNsdXN0ZXItbmFtZQogICAgY2x1c3RlcjoKICAgICAgc2VydmVyOiBodHRwczovL2FwaS5jbHVzdGVyLW5hbWUua3ltYS1kZXYuc2hvb3QuY2FuYXJ5Lms4cy1oYW5hLm9uZGVtYW5kLmNvbQogICAgICBjZXJ0aWZpY2F0ZS1hdXRob3JpdHktZGF0YTogPi0KICAgICAgICBMUzB0TFMxQ1JVZEpUaUJEUlZKVVNVWkpRMEZVUlMwdExTMHQKdXNlcnM6CiAgLSBuYW1lOiBzaG9vdC0ta3ltYS1kZXYtLWNsdXN0ZXItbmFtZS10b2tlbgogICAgdXNlcjoKICAgICAgdG9rZW46ID4tCiAgICAgICAgdE9rRW4K",
						"shootName": "sh1",
						"shootDomain": "sh1.avs.sap.nothing"
					}
		}`)
	opID := suite.DecodeOperationID(resp)

	// then
	suite.WaitForOperationState(opID, domain.Succeeded)

	// get instance OSB API call
	// when
	resp = suite.CallAPI("GET", fmt.Sprintf("oauth/v2/service_instances/%s", iid), ``)
	r, e := io.ReadAll(resp.Body)

	// then
	require.NoError(t, e)
	assert.JSONEq(t, `{
  "service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
  "plan_id": "03e3cb66-a4c6-4c6a-b4b0-5d42224debea",
  "parameters": {
    "plan_id": "03e3cb66-a4c6-4c6a-b4b0-5d42224debea",
    "service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
    "ers_context": {
      "subaccount_id": "sub-id",
      "globalaccount_id": "g-account-id",
      "user_id": "john.smith@email.com"
    },
    "parameters": {
      "name": "testing-cluster",
      "shootName": "sh1",
      "shootDomain": "sh1.avs.sap.nothing"
    },
    "platform_region": "",
    "platform_provider": "unknown"
  },
  "metadata": {
    "labels": {
      "Name": "testing-cluster"
    }
  }
}`, string(r))

}

func TestProvisioning_TrialAtEU(t *testing.T) {
	// The region eu-central-1 is taken because the default Trial Provider  is set to AWS.
	// Other Hyperscalers will have different default regions.

	// given
	suite := NewBrokerSuiteTest(t)
	defer suite.TearDown()
	iid := uuid.New().String()

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/cf-eu11/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "7d55d31d-35ae-4438-bf13-6ffdfa107d9f",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "whitelisted-global-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster"
					}
		}`)
	opID := suite.DecodeOperationID(resp)
	suite.processKIMProvisioningByOperationID(opID)
	suite.WaitForOperationState(opID, domain.Succeeded)

	// then
	runtime := suite.GetRuntimeResourceByInstanceID(iid)
	assert.Equal(t, "eu-central-1", runtime.Spec.Shoot.Region)
	suite.AssertKymaLabelsExist(opID, map[string]string{
		"kyma-project.io/region":          "eu-central-1",
		"kyma-project.io/provider":        "AWS",
		"kyma-project.io/platform-region": "cf-eu11",
	})

}

func TestProvisioning_ClusterParameters(t *testing.T) {
	for tn, tc := range map[string]struct {
		planID                       string
		platformRegionPart           string
		region                       string
		multiZone                    bool
		controlPlaneFailureTolerance string
		useSmallerMachineTypes       bool

		expectedZonesCount           *int
		expectedProvider             string
		expectedMinimalNumberOfNodes int
		expectedMaximumNumberOfNodes int
		expectedMachineType          string
		expectedSubscriptionName     string
		expectedVolumeSize           string
		expectedZones                []string
	}{
		"Regular trial": {
			planID: broker.TrialPlanID,

			expectedMinimalNumberOfNodes: 1,
			expectedMaximumNumberOfNodes: 1,
			expectedMachineType:          "m5.xlarge",
			expectedProvider:             "aws",
			expectedSubscriptionName:     "sb-aws-shared",
			expectedVolumeSize:           "50Gi",
		},
		"Regular trial with smaller machines": {
			planID:                 broker.TrialPlanID,
			useSmallerMachineTypes: true,

			expectedMinimalNumberOfNodes: 1,
			expectedMaximumNumberOfNodes: 1,
			expectedMachineType:          "m6i.large",
			expectedProvider:             "aws",
			expectedSubscriptionName:     "sb-aws-shared",
		},
		"Freemium aws": {
			planID:             broker.FreemiumPlanID,
			region:             "eu-central-1",
			platformRegionPart: "cf-eu10/",

			expectedMinimalNumberOfNodes: 1,
			expectedMaximumNumberOfNodes: 1,
			expectedProvider:             "aws",

			expectedMachineType:      "m5.xlarge",
			expectedSubscriptionName: "sb-aws",
		},
		"Freemium aws with smaller machines": {
			planID:                 broker.FreemiumPlanID,
			platformRegionPart:     "cf-eu10/",
			useSmallerMachineTypes: true,
			region:                 "eu-central-1",

			expectedMinimalNumberOfNodes: 1,
			expectedMaximumNumberOfNodes: 1,
			expectedProvider:             "aws",
			expectedMachineType:          "m6i.large",
			expectedSubscriptionName:     "sb-aws",
		},
		"Freemium azure": {
			planID:             broker.FreemiumPlanID,
			platformRegionPart: "cf-eu21/",
			region:             "westeurope",

			expectedMinimalNumberOfNodes: 1,
			expectedMaximumNumberOfNodes: 1,
			expectedProvider:             "azure",
			expectedMachineType:          "Standard_D4s_v5",
			expectedSubscriptionName:     "sb-azure",
		},
		"Freemium azure with smaller machines": {
			planID:                 broker.FreemiumPlanID,
			useSmallerMachineTypes: true,
			platformRegionPart:     "cf-eu21/",
			region:                 "westeurope",

			expectedMinimalNumberOfNodes: 1,
			expectedMaximumNumberOfNodes: 1,
			expectedProvider:             "azure",
			expectedMachineType:          "Standard_D2s_v5",
			expectedSubscriptionName:     "sb-azure",
		},
		"Production Azure": {
			planID:                       broker.AzurePlanID,
			region:                       "westeurope",
			multiZone:                    false,
			controlPlaneFailureTolerance: "zone",

			expectedZonesCount:           ptr.Integer(1),
			expectedMinimalNumberOfNodes: 3,
			expectedMaximumNumberOfNodes: 20,
			expectedMachineType:          provider.DefaultAzureMachineType,
			expectedProvider:             "azure",
			expectedSubscriptionName:     "sb-azure",
			expectedVolumeSize:           "82Gi",
		},
		"Production Multi-AZ Azure": {
			planID:                       broker.AzurePlanID,
			region:                       "westeurope",
			multiZone:                    true,
			controlPlaneFailureTolerance: "zone",

			expectedZonesCount:           ptr.Integer(3),
			expectedMinimalNumberOfNodes: 3,
			expectedMaximumNumberOfNodes: 20,
			expectedMachineType:          provider.DefaultAzureMachineType,
			expectedProvider:             "azure",
			expectedSubscriptionName:     "sb-azure",
			expectedZones:                []string{"1", "2", "3"},
		},
		"Production AWS": {
			planID:                       broker.AWSPlanID,
			region:                       "us-east-1",
			multiZone:                    false,
			controlPlaneFailureTolerance: "zone",

			expectedZonesCount:           ptr.Integer(1),
			expectedMinimalNumberOfNodes: 3,
			expectedMaximumNumberOfNodes: 20,
			expectedMachineType:          provider.DefaultAWSMachineType,
			expectedProvider:             "aws",

			expectedSubscriptionName: "sb-aws",
		},
		"Production Multi-AZ AWS": {
			planID:                       broker.AWSPlanID,
			region:                       "sa-east-1",
			multiZone:                    true,
			controlPlaneFailureTolerance: "zone",

			expectedZonesCount:           ptr.Integer(3),
			expectedMinimalNumberOfNodes: 3,
			expectedMaximumNumberOfNodes: 20,
			expectedMachineType:          provider.DefaultAWSMachineType,
			expectedProvider:             "aws",
			expectedSubscriptionName:     "sb-aws",
			expectedZones:                []string{"sa-east-1a", "sa-east-1b", "sa-east-1c"},
		},
		"Production GCP": {
			planID:                       broker.GCPPlanID,
			region:                       "us-central1",
			multiZone:                    false,
			controlPlaneFailureTolerance: "zone",

			expectedZonesCount:           ptr.Integer(1),
			expectedMinimalNumberOfNodes: 3,
			expectedMaximumNumberOfNodes: 20,
			expectedMachineType:          provider.DefaultGCPMachineType,
			expectedProvider:             "gcp",
			expectedSubscriptionName:     "sb-gcp",
		},
		"Production GCP KSA": {
			planID:                       broker.GCPPlanID,
			platformRegionPart:           "cf-sa30/",
			region:                       "me-central2",
			multiZone:                    false,
			controlPlaneFailureTolerance: "zone",

			expectedZonesCount:           ptr.Integer(1),
			expectedMinimalNumberOfNodes: 3,
			expectedMaximumNumberOfNodes: 20,
			expectedMachineType:          provider.DefaultGCPMachineType,
			expectedProvider:             "gcp",
			expectedSubscriptionName:     "sb-gcp_cf-sa30",
		},
		"Production Multi-AZ GCP": {
			planID:                       broker.GCPPlanID,
			region:                       "us-central1",
			multiZone:                    true,
			controlPlaneFailureTolerance: "zone",

			expectedZonesCount:           ptr.Integer(3),
			expectedMinimalNumberOfNodes: 3,
			expectedMaximumNumberOfNodes: 20,
			expectedMachineType:          provider.DefaultGCPMachineType,
			expectedProvider:             "gcp",
			expectedSubscriptionName:     "sb-gcp",
		},
		"sap converged cloud eu-de-1": {
			planID: broker.SapConvergedCloudPlanID,
			region: "eu-de-1",
			// this is mandatory because the plan is not existing if the platform region is not in the list (cmd/broker/testdata/old-sap-converged-cloud-region-mappings)
			platformRegionPart:           "cf-eu20/",
			multiZone:                    true,
			controlPlaneFailureTolerance: "zone",

			expectedZonesCount:           ptr.Integer(3),
			expectedMinimalNumberOfNodes: 3,
			expectedMaximumNumberOfNodes: 20,
			expectedMachineType:          provider.DefaultSapConvergedCloudMachineType,
			expectedProvider:             "openstack",
			expectedSubscriptionName:     "sb-openstack_eu-de-1",

			expectedZones: []string{"eu-de-1a", "eu-de-1b", "eu-de-1d"},
		},
		"sap converged cloud eu-de-2": {
			planID:                       broker.SapConvergedCloudPlanID,
			region:                       "eu-de-2",
			platformRegionPart:           "cf-eu10/",
			multiZone:                    true,
			controlPlaneFailureTolerance: "zone",

			// available zones are defined in the internal/provider/zones.go (sapConvergedCloudZones)
			expectedZonesCount:           ptr.Integer(2),
			expectedMinimalNumberOfNodes: 3,
			expectedMaximumNumberOfNodes: 20,
			expectedMachineType:          provider.DefaultSapConvergedCloudMachineType,
			expectedProvider:             "openstack",
			expectedSubscriptionName:     "sb-openstack_eu-de-2",
		},
	} {
		t.Run(tn, func(t *testing.T) {
			// given
			cfg := fixConfig()
			if tc.useSmallerMachineTypes {
				cfg.InfrastructureManager.UseSmallerMachineTypes = true
			}
			cfg.InfrastructureManager.MultiZoneCluster = tc.multiZone
			suite := NewBrokerSuiteTestWithConfig(t, cfg)
			defer suite.TearDown()
			iid := uuid.New().String()

			// when
			regionParam := ""
			if tc.region != "" {
				regionParam = fmt.Sprintf(`"region": "%s",`, tc.region)
			}
			resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/%sv2/service_instances/%s?accepts_incomplete=true", tc.platformRegionPart, iid),
				fmt.Sprintf(`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "%s",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						%s
						"name": "testing-cluster"
					}
		}`, tc.planID, regionParam))
			require.Equal(t, http.StatusAccepted, resp.StatusCode)
			opID := suite.DecodeOperationID(resp)

			// then
			suite.processKIMProvisioningByOperationID(opID)

			// then
			suite.WaitForProvisioningState(opID, domain.Succeeded)

			runtimeCR := suite.GetRuntimeResourceByInstanceID(iid)
			assert.Equal(t, tc.expectedProvider, runtimeCR.Spec.Shoot.Provider.Type)
			assert.Equal(t, tc.expectedMinimalNumberOfNodes, int(runtimeCR.Spec.Shoot.Provider.Workers[0].Minimum))
			assert.Equal(t, tc.expectedMaximumNumberOfNodes, int(runtimeCR.Spec.Shoot.Provider.Workers[0].Maximum))
			assert.Equal(t, tc.expectedMachineType, runtimeCR.Spec.Shoot.Provider.Workers[0].Machine.Type)
			if tc.expectedZonesCount != nil {
				assert.Equal(t, *tc.expectedZonesCount, len(runtimeCR.Spec.Shoot.Provider.Workers[0].Zones))
			}
			if tc.controlPlaneFailureTolerance != "" {
				assert.Equal(t, tc.controlPlaneFailureTolerance, string(runtimeCR.Spec.Shoot.ControlPlane.HighAvailability.FailureTolerance.Type))
			} else {
				assert.Nil(t, runtimeCR.Spec.Shoot.ControlPlane)
			}

			if tc.expectedSubscriptionName != "" {
				assert.Equal(t, tc.expectedSubscriptionName, runtimeCR.Spec.Shoot.SecretBindingName)
			}
			if tc.expectedVolumeSize != "" {
				assert.Equal(t, tc.expectedVolumeSize, runtimeCR.Spec.Shoot.Provider.Workers[0].Volume.VolumeSize)
			}
			if len(tc.expectedZones) > 0 {
				assert.ElementsMatch(t, tc.expectedZones, runtimeCR.Spec.Shoot.Provider.Workers[0].Zones)
			}
		})

	}
}

func TestProvisioning_OIDCValues(t *testing.T) {

	t.Run("should apply default OIDC values when OIDC object is nil", func(t *testing.T) {
		// given
		suite := NewBrokerSuiteTest(t)
		defer suite.TearDown()
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			fmt.Sprintf(`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "%s",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"region": "eu-central-1",
						"name": "testing-cluster"
					}
		}`, broker.AWSPlanID))

		opID := suite.DecodeOperationID(resp)
		suite.processKIMProvisioningByOperationID(opID)
		suite.WaitForOperationState(opID, domain.Succeeded)

		// then
		runtime := suite.GetRuntimeResourceByInstanceID(iid)
		gotOIDC := runtime.Spec.Shoot.Kubernetes.KubeAPIServer.AdditionalOidcConfig

		assert.Equal(t, defaultOIDCValues().ClientID, *(*gotOIDC)[0].ClientID)
		assert.Equal(t, defaultOIDCValues().GroupsClaim, *(*gotOIDC)[0].GroupsClaim)
		assert.Equal(t, defaultOIDCValues().IssuerURL, *(*gotOIDC)[0].IssuerURL)
		assert.Equal(t, defaultOIDCValues().SigningAlgs, (*gotOIDC)[0].SigningAlgs)
		assert.Equal(t, defaultOIDCValues().UsernameClaim, *(*gotOIDC)[0].UsernameClaim)
		assert.Equal(t, defaultOIDCValues().UsernamePrefix, *(*gotOIDC)[0].UsernamePrefix)
		assert.Equal(t, defaultOIDCValues().GroupsPrefix, *(*gotOIDC)[0].GroupsPrefix)
		assert.Nil(t, (*gotOIDC)[0].RequiredClaims)
	})

	t.Run("should apply OIDC values list with one element", func(t *testing.T) {
		// given
		cfg := fixConfig()
		suite := NewBrokerSuiteTestWithConfig(t, cfg)
		defer suite.TearDown()
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			fmt.Sprintf(`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "%s",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"region": "eu-central-1",
						"name": "testing-cluster",
						"oidc": {
							"list": [
								{
									"clientID": "fake-client-id-1",
									"groupsClaim": "fakeGroups",
									"issuerURL": "https://testurl.local",
									"signingAlgs": ["RS256", "RS384"],
									"usernameClaim": "fakeUsernameClaim",
									"groupsPrefix": "groups-prefix",
									"usernamePrefix": "::",
									"requiredClaims": ["claim=value"]
								}
							]
						}
					}
		}`, broker.AWSPlanID))

		opID := suite.DecodeOperationID(resp)
		suite.processKIMProvisioningByOperationID(opID)
		suite.WaitForOperationState(opID, domain.Succeeded)

		// then
		runtime := suite.GetRuntimeResourceByInstanceID(iid)
		gotOIDC := runtime.Spec.Shoot.Kubernetes.KubeAPIServer.AdditionalOidcConfig

		assert.Equal(t, ptr.String("fake-client-id-1"), (*gotOIDC)[0].ClientID)
		assert.Equal(t, ptr.String("fakeGroups"), (*gotOIDC)[0].GroupsClaim)
		assert.Equal(t, ptr.String("https://testurl.local"), (*gotOIDC)[0].IssuerURL)
		assert.Equal(t, []string{"RS256", "RS384"}, (*gotOIDC)[0].SigningAlgs)
		assert.Equal(t, ptr.String("fakeUsernameClaim"), (*gotOIDC)[0].UsernameClaim)
		assert.Equal(t, ptr.String("::"), (*gotOIDC)[0].UsernamePrefix)
		assert.Equal(t, "groups-prefix", *(*gotOIDC)[0].GroupsPrefix)
		assert.Equal(t, map[string]string{"claim": "value"}, (*gotOIDC)[0].RequiredClaims)
	})

	t.Run("should apply empty OIDC list", func(t *testing.T) {
		// given
		cfg := fixConfig()
		suite := NewBrokerSuiteTestWithConfig(t, cfg)
		defer suite.TearDown()
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			fmt.Sprintf(`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "%s",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"region": "eu-central-1",
						"name": "testing-cluster",
						"oidc": {
							"list": []
						}
					}
		}`, broker.AWSPlanID))

		opID := suite.DecodeOperationID(resp)
		suite.processKIMProvisioningByOperationID(opID)
		suite.WaitForOperationState(opID, domain.Succeeded)

		// then
		runtime := suite.GetRuntimeResourceByInstanceID(iid)
		gotOIDC := runtime.Spec.Shoot.Kubernetes.KubeAPIServer.AdditionalOidcConfig
		assert.Empty(t, gotOIDC)
	})

	t.Run("should apply default OIDC values when all OIDC object's fields are empty", func(t *testing.T) {
		// given
		suite := NewBrokerSuiteTest(t)
		defer suite.TearDown()
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			fmt.Sprintf(`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "%s",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"region": "eu-central-1",
						"name": "testing-cluster",
						"oidc": {
							"clientID": "",
							"groupsClaim": "",
							"issuerURL": "",
							"singingAlgs": [],
							"usernameClaim": "",
							"usernamePrefix": ""
						}
					}
		}`, broker.AWSPlanID))

		opID := suite.DecodeOperationID(resp)
		suite.processKIMProvisioningByOperationID(opID)
		suite.WaitForOperationState(opID, domain.Succeeded)

		// then
		runtime := suite.GetRuntimeResourceByInstanceID(iid)
		gotOIDC := runtime.Spec.Shoot.Kubernetes.KubeAPIServer.AdditionalOidcConfig

		assert.Equal(t, defaultOIDCValues().ClientID, *(*gotOIDC)[0].ClientID)
		assert.Equal(t, defaultOIDCValues().GroupsClaim, *(*gotOIDC)[0].GroupsClaim)
		assert.Equal(t, defaultOIDCValues().IssuerURL, *(*gotOIDC)[0].IssuerURL)
		assert.Equal(t, defaultOIDCValues().SigningAlgs, (*gotOIDC)[0].SigningAlgs)
		assert.Equal(t, defaultOIDCValues().UsernameClaim, *(*gotOIDC)[0].UsernameClaim)
		assert.Equal(t, defaultOIDCValues().UsernamePrefix, *(*gotOIDC)[0].UsernamePrefix)
		assert.Equal(t, defaultOIDCValues().GroupsPrefix, *(*gotOIDC)[0].GroupsPrefix)

	})

	t.Run("should apply provided OIDC configuration", func(t *testing.T) {
		// given
		suite := NewBrokerSuiteTest(t)
		defer suite.TearDown()
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			fmt.Sprintf(`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "%s",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"region": "eu-central-1",
						"name": "testing-cluster",
						"oidc": {
							"clientID": "fake-client-id-1",
							"groupsClaim": "fakeGroups",
							"issuerURL": "https://testurl.local",
							"signingAlgs": ["RS256", "RS384"],
							"usernameClaim": "fakeUsernameClaim",
							"usernamePrefix": "::",
							"groupsPrefix": "SAPSE:"
						}
					}
		}`, broker.AWSPlanID))

		opID := suite.DecodeOperationID(resp)
		suite.processKIMProvisioningByOperationID(opID)
		suite.WaitForOperationState(opID, domain.Succeeded)

		// then
		runtime := suite.GetRuntimeResourceByInstanceID(iid)
		gotOIDC := runtime.Spec.Shoot.Kubernetes.KubeAPIServer.AdditionalOidcConfig

		assert.Equal(t, "fake-client-id-1", *(*gotOIDC)[0].ClientID)
		assert.Equal(t, "fakeGroups", *(*gotOIDC)[0].GroupsClaim)
		assert.Equal(t, "https://testurl.local", *(*gotOIDC)[0].IssuerURL)
		assert.Equal(t, []string{"RS256", "RS384"}, (*gotOIDC)[0].SigningAlgs)
		assert.Equal(t, "fakeUsernameClaim", *(*gotOIDC)[0].UsernameClaim)
		assert.Equal(t, "::", *(*gotOIDC)[0].UsernamePrefix)
		assert.Equal(t, "SAPSE:", *(*gotOIDC)[0].GroupsPrefix)
	})

	t.Run("should apply default OIDC values when all OIDC object's fields are not present", func(t *testing.T) {
		// given
		cfg := fixConfig()
		cfg.Broker.IncludeAdditionalParamsInSchema = false
		suite := NewBrokerSuiteTestWithConfig(t, cfg)

		defer suite.TearDown()
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			fmt.Sprintf(`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "%s",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"region": "eu-central-1",
						"name": "testing-cluster",
						"oidc": {
						}
					}
		}`, broker.AWSPlanID))

		opID := suite.DecodeOperationID(resp)
		suite.processKIMProvisioningByOperationID(opID)
		suite.WaitForOperationState(opID, domain.Succeeded)

		// then
		runtime := suite.GetRuntimeResourceByInstanceID(iid)
		gotOIDC := runtime.Spec.Shoot.Kubernetes.KubeAPIServer.AdditionalOidcConfig

		assert.Equal(t, defaultOIDCValues().ClientID, *(*gotOIDC)[0].ClientID)
		assert.Equal(t, defaultOIDCValues().GroupsClaim, *(*gotOIDC)[0].GroupsClaim)
		assert.Equal(t, defaultOIDCValues().IssuerURL, *(*gotOIDC)[0].IssuerURL)
		assert.Equal(t, defaultOIDCValues().SigningAlgs, (*gotOIDC)[0].SigningAlgs)
		assert.Equal(t, defaultOIDCValues().UsernameClaim, *(*gotOIDC)[0].UsernameClaim)
		assert.Equal(t, defaultOIDCValues().UsernamePrefix, *(*gotOIDC)[0].UsernamePrefix)
	})
	t.Run("should reject non base64 JWKS value", func(t *testing.T) {
		// given
		cfg := fixConfig()
		suite := NewBrokerSuiteTestWithConfig(t, cfg)
		defer suite.TearDown()
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			fmt.Sprintf(`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "%s",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"region": "eu-central-1",
						"name": "testing-cluster",
						"oidc": {
							"clientID": "fake-client-id-1",
							"groupsClaim": "fakeGroups",
							"issuerURL": "https://testurl.local",
							"signingAlgs": ["RS256", "RS384"],
							"usernameClaim": "fakeUsernameClaim",
							"usernamePrefix": "::",
							"encodedJwksArray": "not-base64"
						}
					}
		}`, broker.AWSPlanID))
		parsedResponse := suite.ReadResponse(resp)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Contains(t, string(parsedResponse), "encodedJwksArray must be a valid base64-encoded value or set to '-' to disable it if it was used previously")
	})
}

func TestProvisioning_RuntimeAdministrators(t *testing.T) {
	t.Run("should use UserID as default value for admins list", func(t *testing.T) {
		// given
		suite := NewBrokerSuiteTest(t, "2.0")
		defer suite.TearDown()
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "eu-central-1"
					}
		}`)
		opID := suite.DecodeOperationID(resp)

		suite.WaitForProvisioningState(opID, domain.InProgress)
		suite.processKIMProvisioningByOperationID(opID)

		// then
		suite.WaitForProvisioningState(opID, domain.Succeeded)
		suite.AssertRuntimeAdminsByInstanceID(iid, []string{"john.smith@email.com"})
	})

	t.Run("should apply new admins list", func(t *testing.T) {
		// given
		suite := NewBrokerSuiteTest(t, "2.0")
		defer suite.TearDown()
		expectedAdmins := []string{"admin1@test.com", "admin2@test.com"}
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "eu-central-1",
						"administrators": ["admin1@test.com", "admin2@test.com"]
					}
		}`)
		opID := suite.DecodeOperationID(resp)

		suite.WaitForProvisioningState(opID, domain.InProgress)
		suite.processKIMProvisioningByOperationID(opID)

		// then
		suite.WaitForProvisioningState(opID, domain.Succeeded)
		suite.AssertRuntimeAdminsByInstanceID(iid, expectedAdmins)
	})

	t.Run("should apply empty admin value (list is not empty)", func(t *testing.T) {
		// given
		suite := NewBrokerSuiteTest(t, "2.0")
		defer suite.TearDown()
		expectedAdmins := []string{""}
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "eu-central-1",
						"administrators": [""]
					}
		}`)
		opID := suite.DecodeOperationID(resp)

		suite.WaitForProvisioningState(opID, domain.InProgress)
		suite.processKIMProvisioningByOperationID(opID)

		// then
		suite.WaitForProvisioningState(opID, domain.Succeeded)
		suite.AssertRuntimeAdminsByInstanceID(iid, expectedAdmins)
	})
}

func TestProvisioning_WithNetworkFilters(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t, "2.0")
	defer suite.TearDown()
	iid := uuid.New().String()

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "eu-central-1",
						"ingressFiltering": true
					}
		}`)
	opID := suite.DecodeOperationID(resp)
	require.NotEmpty(t, opID)
	suite.processKIMProvisioningByOperationID(opID)
	instance := suite.GetInstance(iid)

	// then
	suite.AssertNetworkFiltering(iid, true, true)
	assert.Nil(suite.t, instance.Parameters.ErsContext.LicenseType)
}

func TestProvisioning_NetworkFilter_External_True(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t, "2.0")
	defer suite.TearDown()
	iid := uuid.New().String()

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"license_type": "CUSTOMER",
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "eu-central-1",
						"ingressFiltering": true
					}
		}`)
	assert.Equal(t, resp.StatusCode, http.StatusBadRequest)
	parsedResponse := suite.ReadResponse(resp)
	assert.Contains(t, string(parsedResponse), "ingress filtering option is not available")
}

func TestProvisioning_NetworkFilter_External_False(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t, "2.0")
	defer suite.TearDown()
	iid := uuid.New().String()

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"license_type": "CUSTOMER",
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "eu-central-1",
						"ingressFiltering": false
					}
		}`)
	assert.Equal(t, resp.StatusCode, http.StatusAccepted)
}

func TestProvisioning_Modules(t *testing.T) {

	const defaultModules = "kyma-with-keda-and-btp-operator.yaml"

	t.Run("with given custom list of modules [btp-operator, ked] all set", func(t *testing.T) {
		// given
		suite := NewBrokerSuiteTest(t)
		defer suite.TearDown()
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
							"globalaccount_id": "whitelisted-global-account-id",
							"subaccount_id": "sub-id",
							"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "test",
						"region": "eu-central-1",
						"modules": {
							"list": [
								{
									"name": "btp-operator",
									"customResourcePolicy": "Ignore",
									"channel": "fast"
								},
								{
									"name": "keda",
									"customResourcePolicy": "CreateAndDelete",
									"channel": "regular"
								}
							]
						}
					}
				}`)
		opID := suite.DecodeOperationID(resp)

		suite.processKIMProvisioningByOperationID(opID)

		suite.WaitForOperationState(opID, domain.Succeeded)
		op, err := suite.db.Operations().GetOperationByID(opID)
		assert.NoError(t, err)
		assert.YAMLEq(t, internal.GetKymaTemplateForTests(t, "kyma-with-keda-and-btp-operator-all-params-set.yaml"), op.KymaTemplate)
	})

	t.Run("with given custom list of modules [btp-operator, ked] with channel and crPolicy as empty", func(t *testing.T) {
		// given
		suite := NewBrokerSuiteTest(t)
		defer suite.TearDown()
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
							"globalaccount_id": "whitelisted-global-account-id",
							"subaccount_id": "sub-id",
							"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "test",
						"region": "eu-central-1",
						"modules": {
							"list": [
								{
									"name": "btp-operator"
								},
								{
									"name": "keda"
								}
							]
						}
					}
				}`)
		opID := suite.DecodeOperationID(resp)

		suite.processKIMProvisioningByOperationID(opID)

		suite.WaitForOperationState(opID, domain.Succeeded)
		op, err := suite.db.Operations().GetOperationByID(opID)
		assert.NoError(t, err)
		assert.YAMLEq(t, internal.GetKymaTemplateForTests(t, "kyma-with-keda-and-btp-operator-only-name.yaml"), op.KymaTemplate)
	})

	t.Run("with given custom list of modules [btp-operator, ked] with channel and crPolicy as not set", func(t *testing.T) {
		// given
		suite := NewBrokerSuiteTest(t)
		defer suite.TearDown()
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
							"globalaccount_id": "whitelisted-global-account-id",
							"subaccount_id": "sub-id",
							"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "test",
						"region": "eu-central-1",
						"modules": {
							"list": [
								{
									"name": "btp-operator",
									"customResourcePolicy": "",
									"channel": ""
								},
								{
									"name": "keda",
									"customResourcePolicy": "",
									"channel": ""
								}
							]
						}
					}
				}`)
		opID := suite.DecodeOperationID(resp)

		suite.processKIMProvisioningByOperationID(opID)

		suite.WaitForOperationState(opID, domain.Succeeded)
		op, err := suite.db.Operations().GetOperationByID(opID)
		assert.NoError(t, err)
		assert.YAMLEq(t, internal.GetKymaTemplateForTests(t, "kyma-with-keda-and-btp-operator-only-name.yaml"), op.KymaTemplate)
	})

	t.Run("with given empty list of modules", func(t *testing.T) {
		// given
		suite := NewBrokerSuiteTest(t)
		defer suite.TearDown()
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
							"globalaccount_id": "whitelisted-global-account-id",
							"subaccount_id": "sub-id",
							"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "test",
						"region": "eu-central-1",
						"modules": {
							"list": []
						}
					}
				}`)
		opID := suite.DecodeOperationID(resp)

		suite.processKIMProvisioningByOperationID(opID)

		suite.WaitForOperationState(opID, domain.Succeeded)
		op, err := suite.db.Operations().GetOperationByID(opID)
		assert.NoError(t, err)
		assert.YAMLEq(t, internal.GetKymaTemplateForTests(t, "kyma-no-modules.yaml"), op.KymaTemplate)
	})

	t.Run("with given default as false", func(t *testing.T) {
		// given
		suite := NewBrokerSuiteTest(t)
		defer suite.TearDown()
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
							"globalaccount_id": "whitelisted-global-account-id",
							"subaccount_id": "sub-id",
							"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "test",
						"region": "eu-central-1",
						"modules": {
							"default": false
						}
					}
				}`)

		opID := suite.DecodeOperationID(resp)

		suite.processKIMProvisioningByOperationID(opID)

		suite.WaitForOperationState(opID, domain.Succeeded)
		op, err := suite.db.Operations().GetOperationByID(opID)
		assert.NoError(t, err)
		assert.YAMLEq(t, internal.GetKymaTemplateForTests(t, "kyma-no-modules.yaml"), op.KymaTemplate)
	})

	t.Run("with given default as true", func(t *testing.T) {
		// given
		suite := NewBrokerSuiteTest(t)
		defer suite.TearDown()
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "7d55d31d-35ae-4438-bf13-6ffdfa107d9f",
					"context": {
							"globalaccount_id": "whitelisted-global-account-id",
							"subaccount_id": "sub-id",
							"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "test",
						"modules": {
							"default": true
						}
					}
				}`)

		opID := suite.DecodeOperationID(resp)

		suite.processKIMProvisioningByOperationID(opID)

		suite.WaitForOperationState(opID, domain.Succeeded)
		op, err := suite.db.Operations().GetOperationByID(opID)
		assert.NoError(t, err)
		assert.YAMLEq(t, internal.GetKymaTemplateForTests(t, defaultModules), op.KymaTemplate)
	})

	t.Run("oneOf validation fail when two params are set", func(t *testing.T) {
		// given
		suite := NewBrokerSuiteTest(t)
		defer suite.TearDown()
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
			"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
			"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
			"context": {
					"globalaccount_id": "whitelisted-global-account-id",
					"subaccount_id": "sub-id",
					"user_id": "john.smith@email.com"
			},
			"parameters": {
				"name": "test",
				"region": "eu-central-1",
				"modules": {
					"default": false,
					"list": [
						{
							"name": "btp-operator",
							"channel": "regular",
							"customResourcePolicy": "CreateAndDelete"
						},
						{
							"name": "keda",
							"channel": "fast",
							"customResourcePolicy": "Ignore"
						}
					]
				}
			}
		}`)

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("oneOf validation fail when no any modules param is set", func(t *testing.T) {
		// given
		suite := NewBrokerSuiteTest(t)
		defer suite.TearDown()
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
							"globalaccount_id": "whitelisted-global-account-id",
							"subaccount_id": "sub-id",
							"user_id": "john.smith@email.com"
					},
					"parameters": {
							"name": "test",
							"region": "eu-central-1",
							"modules": {}
						}
					}
				}`)

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("validation fail due to incorrect channel/crPolicy", func(t *testing.T) {
		// given
		suite := NewBrokerSuiteTest(t)
		defer suite.TearDown()
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
							"globalaccount_id": "whitelisted-global-account-id",
							"subaccount_id": "sub-id",
							"user_id": "john.smith@email.com"
					},
					"parameters": {
							"name": "test",
							"region": "eu-central-1",
							"modules": {
								"list": [
									{
										"name": "btp-operator",
										"channel": "regularWrong",
										"customResourcePolicy": "CreateAndDeleteWrong"
									}
								]
							}
						}
					}
				}`)

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("validation fail when name not passed", func(t *testing.T) {
		// given
		suite := NewBrokerSuiteTest(t)
		defer suite.TearDown()
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
							"globalaccount_id": "whitelisted-global-account-id",
							"subaccount_id": "sub-id",
							"user_id": "john.smith@email.com"
					},
					"parameters": {
							"name": "test",
							"region": "eu-central-1",
							"modules": {
								"list": [
									{
										"channel": "regularWrong",
										"customResourcePolicy": "CreateAndDeleteWrong"
									}
								]
							}
						}
					}
				}`)

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("validation fail when name empty", func(t *testing.T) {
		// given
		suite := NewBrokerSuiteTest(t)
		defer suite.TearDown()
		iid := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/v2/service_instances/%s?accepts_incomplete=true", iid),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
							"globalaccount_id": "whitelisted-global-account-id",
							"subaccount_id": "sub-id",
							"user_id": "john.smith@email.com"
					},
					"parameters": {
							"name": "test",
							"region": "eu-central-1",
							"modules": {
								"list": [
									{
										"name": "",
										"channel": "regularWrong",
										"customResourcePolicy": "CreateAndDeleteWrong"
									}
								]
							}
						}
					}
				}`)

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestProvisioningWithAdditionalWorkerNodePools(t *testing.T) {
	// given
	cfg := fixConfig()

	suite := NewBrokerSuiteTestWithConfig(t, cfg)
	defer suite.TearDown()
	iid := uuid.New().String()

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/cf-eu21/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "eu-central-1",
						"additionalWorkerNodePools": [
							{
								"name": "name-1",
								"machineType": "m6i.large",
								"haZones": true,
								"autoScalerMin": 3,
								"autoScalerMax": 20
							},
							{
								"name": "name-2",
								"machineType": "m5.large",
								"haZones": false,
								"autoScalerMin": 1,
								"autoScalerMax": 1
							}
						]
					}
		}`)

	opID := suite.DecodeOperationID(resp)
	suite.processKIMProvisioningByInstanceID(iid)

	// then
	suite.WaitForOperationState(opID, domain.Succeeded)
	runtime := suite.GetRuntimeResourceByInstanceID(iid)
	assert.Len(t, *runtime.Spec.Shoot.Provider.AdditionalWorkers, 2)
	suite.assertAdditionalWorkerIsCreated(t, runtime.Spec.Shoot.Provider, "name-1", "m6i.large", 3, 20, 3)
	suite.assertAdditionalWorkerIsCreated(t, runtime.Spec.Shoot.Provider, "name-2", "m5.large", 1, 1, 1)

	resp = suite.CallAPI("GET", fmt.Sprintf("runtimes?runtime_config=true&account=%s&subaccount=%s&state=provisioned", "g-account-id", "sub-id"), "")
	response, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	var runtimes pkg.RuntimesPage

	err = json.Unmarshal(response, &runtimes)

	assert.Len(t, runtimes.Data, 1)
	config := runtimes.Data[0].RuntimeConfig
	assert.NotNil(t, config)

	provider, ok, err := unstructured.NestedMap(*config, "spec", "shoot", "provider")
	assert.True(t, ok)
	assert.NoError(t, err)

	workers, ok, err := unstructured.NestedSlice(provider, "workers")
	assert.True(t, ok)
	assert.NoError(t, err)

	additionalWorkers, ok, err := unstructured.NestedSlice(provider, "additionalWorkers")
	assert.True(t, ok)
	assert.NoError(t, err)

	assert.Len(t, workers, 1)
	assert.Len(t, additionalWorkers, 2)

	assert.Equal(t, "cpu-worker-0", workers[0].(map[string]interface{})["name"])
	assert.Equal(t, "name-1", additionalWorkers[0].(map[string]interface{})["name"])
	assert.Equal(t, "name-2", additionalWorkers[1].(map[string]interface{})["name"])

	assert.Equal(t, http.StatusOK, resp.StatusCode)

}

func TestZoneMappingInAdditionalWorkerNodePools(t *testing.T) {
	// given
	cfg := fixConfig()

	suite := NewBrokerSuiteTestWithConfig(t, cfg)
	defer suite.TearDown()
	iid := uuid.New().String()

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/cf-eu21/v2/service_instances/%s?accepts_incomplete=true", iid),
		`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
					"context": {
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "testing-cluster",
						"region": "us-east-1",
						"additionalWorkerNodePools": [
							{
								"name": "name-1",
								"machineType": "c7i.large",
								"haZones": true,
								"autoScalerMin": 3,
								"autoScalerMax": 20
							},
							{
								"name": "name-2",
								"machineType": "g6.xlarge",
								"haZones": false,
								"autoScalerMin": 1,
								"autoScalerMax": 1
							},
							{
								"name": "name-3",
								"machineType": "g4dn.xlarge",
								"haZones": false,
								"autoScalerMin": 1,
								"autoScalerMax": 1
							}
						]
					}
		}`)

	opID := suite.DecodeOperationID(resp)
	suite.processKIMProvisioningByInstanceID(iid)

	// then
	suite.WaitForOperationState(opID, domain.Succeeded)
	runtime := suite.GetRuntimeResourceByInstanceID(iid)
	assert.Len(t, *runtime.Spec.Shoot.Provider.AdditionalWorkers, 3)
	suite.assertAdditionalWorkerZones(t, runtime.Spec.Shoot.Provider, "name-1", 3, "us-east-1w", "us-east-1x", "us-east-1y", "us-east-1z")
	suite.assertAdditionalWorkerZones(t, runtime.Spec.Shoot.Provider, "name-2", 1, "us-east-1x", "us-east-1y")
	suite.assertAdditionalWorkerZones(t, runtime.Spec.Shoot.Provider, "name-3", 1, "us-east-1x")
}

func TestProvisioning_BuildRuntimePlans(t *testing.T) {
	// given
	suite := NewBrokerSuiteTest(t)
	defer suite.TearDown()

	t.Run("should provision instance with build-runtime-aws plan", func(t *testing.T) {
		// given
		instanceID := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf(provisioningRequestPathFormat, instanceID),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "6aae0ff3-89f7-4f12-86de-51466145422e",
					"context": {
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "build-runtime-aws-1",
						"region": "eu-central-1"
					}
		}`)

		opID := suite.DecodeOperationID(resp)
		suite.processKIMProvisioningByOperationID(opID)

		// then
		suite.WaitForOperationState(opID, domain.Succeeded)
		suite.AssertRuntimeResourceLabels(opID)
	})

	t.Run("should provision instance with build-runtime-gcp plan", func(t *testing.T) {
		// given
		instanceID := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf(provisioningRequestPathFormat, instanceID),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "a310cd6b-6452-45a0-935d-d24ab53f9eba",
					"context": {
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "build-runtime-gcp-1",
						"region": "europe-west3"
					}
		}`)

		opID := suite.DecodeOperationID(resp)
		suite.processKIMProvisioningByOperationID(opID)

		// then
		suite.WaitForOperationState(opID, domain.Succeeded)
		suite.AssertRuntimeResourceLabels(opID)
	})

	t.Run("should provision instance with build-runtime-azure plan", func(t *testing.T) {
		// given
		instanceID := uuid.New().String()

		// when
		resp := suite.CallAPI("PUT", fmt.Sprintf(provisioningRequestPathFormat, instanceID),
			`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "499244b4-1bef-48c9-be68-495269899f8e",
					"context": {
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						"name": "build-runtime-azure-1",
						"region": "westeurope"
					}
		}`)

		opID := suite.DecodeOperationID(resp)
		suite.processKIMProvisioningByOperationID(opID)

		// then
		suite.WaitForOperationState(opID, domain.Succeeded)
		suite.AssertRuntimeResourceLabels(opID)
	})
}

func TestProvisioning_ResolveSubscriptionSecretStepEnabled(t *testing.T) {
	for tn, tc := range map[string]struct {
		planID         string
		region         string
		platformRegion string

		expectedProvider         string
		expectedSubscriptionName string
	}{
		"Trial": {
			planID: broker.TrialPlanID,

			expectedProvider:         "aws",
			expectedSubscriptionName: "sb-aws-shared",
		},
		"Freemium aws": {
			planID:         broker.FreemiumPlanID,
			region:         "eu-central-1",
			platformRegion: "cf-eu10",

			expectedProvider:         "aws",
			expectedSubscriptionName: "sb-aws",
		},
		"Freemium azure": {
			planID:         broker.FreemiumPlanID,
			region:         "westeurope",
			platformRegion: "cf-eu21",

			expectedProvider:         "azure",
			expectedSubscriptionName: "sb-azure",
		},
		"Production Azure": {
			planID: broker.AzurePlanID,
			region: "westeurope",

			expectedProvider:         "azure",
			expectedSubscriptionName: "sb-azure",
		},
		"Production AWS": {
			planID: broker.AWSPlanID,
			region: "us-east-1",

			expectedProvider:         "aws",
			expectedSubscriptionName: "sb-aws",
		},
		"Production GCP": {
			planID: broker.GCPPlanID,
			region: "us-central1",

			expectedProvider:         "gcp",
			expectedSubscriptionName: "sb-gcp",
		},
		"Production GCP KSA": {
			planID:         broker.GCPPlanID,
			region:         "me-central2",
			platformRegion: "cf-sa30",

			expectedProvider:         "gcp",
			expectedSubscriptionName: "sb-gcp_cf-sa30",
		},
		"sap converged cloud eu-de-1": {
			planID:         broker.SapConvergedCloudPlanID,
			region:         "eu-de-1",
			platformRegion: "cf-eu20",

			expectedProvider:         "openstack",
			expectedSubscriptionName: "sb-openstack_eu-de-1",
		},
		"sap converged cloud eu-de-2": {
			planID:         broker.SapConvergedCloudPlanID,
			region:         "eu-de-2",
			platformRegion: "cf-eu10",

			expectedProvider:         "openstack",
			expectedSubscriptionName: "sb-openstack_eu-de-2",
		},
	} {
		t.Run(tn, func(t *testing.T) {
			// given
			cfg := fixConfig()
			cfg.Broker.EnablePlans = append(cfg.Broker.EnablePlans, "azure_lite")
			suite := NewBrokerSuiteTestWithConfig(t, cfg)
			defer suite.TearDown()
			iid := uuid.New().String()

			// when
			var platformRegion, clusterRegion string
			if tc.region != "" {
				clusterRegion = fmt.Sprintf(`"region": "%s",`, tc.region)
			}
			if tc.platformRegion != "" {
				platformRegion = fmt.Sprintf("%s/", tc.platformRegion)
			}
			resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/%sv2/service_instances/%s?accepts_incomplete=true", platformRegion, iid),
				fmt.Sprintf(`{
					"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
					"plan_id": "%s",
					"context": {
						"sm_platform_credentials": {
							  "url": "https://sm.url",
							  "credentials": {}
					    },
						"globalaccount_id": "g-account-id",
						"subaccount_id": "sub-id",
						"user_id": "john.smith@email.com"
					},
					"parameters": {
						%s
						"name": "testing-cluster"
					}
		}`, tc.planID, clusterRegion))
			require.Equal(t, http.StatusAccepted, resp.StatusCode)
			opID := suite.DecodeOperationID(resp)

			// then
			suite.processKIMProvisioningByOperationID(opID)

			// then
			suite.WaitForProvisioningState(opID, domain.Succeeded)

			runtimeCR := suite.GetRuntimeResourceByInstanceID(iid)

			assert.Equal(t, tc.expectedSubscriptionName, runtimeCR.Spec.Shoot.SecretBindingName)
		})

	}
}
