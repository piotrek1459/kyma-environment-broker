package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/kyma-project/kyma-environment-broker/common/runtime"
	"github.com/pivotal-cf/brokerapi/v12/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRawParametersInRuntimesResponse(t *testing.T) {
	// given
	cfg := fixConfig()
	suite := NewBrokerSuiteTestWithConfig(t, cfg)
	defer suite.TearDown()
	iid := uuid.New().String()

	// provision with explicit administrators (empty array) to verify Issue 2; use AWS plan so autoScalerMax update is not zeroed out
	provisionResp := suite.CallAPI("PUT", fmt.Sprintf("oauth/cf-eu21/v2/service_instances/%s?accepts_incomplete=true", iid),
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
				"administrators": []
			}
		}`)
	defer func() { _ = provisionResp.Body.Close() }()
	provOpID := suite.DecodeOperationID(provisionResp)
	suite.processKIMProvisioningByOperationID(provOpID)
	suite.WaitForOperationState(provOpID, domain.Succeeded)

	// send update with a single field only
	updateResp := suite.CallAPI("PATCH", fmt.Sprintf("oauth/cf-eu21/v2/service_instances/%s?accepts_incomplete=true&plan_id=361c511f-f939-4621-b228-d0fb79a1fe15&service_id=47c9dcbf-ff30-448e-ab36-d3bad66ba281", iid),
		`{
			"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
			"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
			"context": {
				"globalaccount_id": "g-account-id",
				"subaccount_id": "sub-id",
				"user_id": "john.smith@email.com"
			},
			"parameters": {
				"autoScalerMax": 21
			}
		}`)
	defer func() { _ = updateResp.Body.Close() }()
	require.Equal(t, http.StatusAccepted, updateResp.StatusCode)
	updOpID := suite.DecodeOperationID(updateResp)
	suite.WaitForOperationState(updOpID, domain.Succeeded)

	// when - query runtimes with all operations
	runtimesResp := suite.CallAPI("GET", fmt.Sprintf("runtimes?instance_id=%s&ops=all", iid), "")
	defer func() { _ = runtimesResp.Body.Close() }()

	// then
	require.Equal(t, http.StatusOK, runtimesResp.StatusCode)
	body, err := io.ReadAll(runtimesResp.Body)
	require.NoError(t, err)
	var page runtime.RuntimesPage
	require.NoError(t, json.Unmarshal(body, &page))
	require.Len(t, page.Data, 1)

	rt := page.Data[0]

	t.Run("provisioning rawParameters contains only submitted fields (empty array preserved)", func(t *testing.T) {
		require.NotNil(t, rt.Status.Provisioning)
		var provRaw map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rt.Status.Provisioning.RawParameters, &provRaw))
		assert.Equal(t, json.RawMessage(`"testing-cluster"`), provRaw["name"])
		assert.Contains(t, provRaw, "administrators", "empty array should be preserved")
		// verify it does not contain merged-state-only fields
		assert.NotContains(t, provRaw, "autoScalerMax")
	})

	t.Run("update rawParameters contains only the single submitted field, not full merged state", func(t *testing.T) {
		require.NotNil(t, rt.Status.Update)
		require.Len(t, rt.Status.Update.Data, 1)
		updateOp := rt.Status.Update.Data[0]

		var updRaw map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(updateOp.RawParameters, &updRaw))

		assert.Equal(t, json.RawMessage(`21`), updRaw["autoScalerMax"])
		// the merged state would include name, region, administrators, oidc, etc. — they must not appear
		assert.NotContains(t, updRaw, "name", "merged-state fields must not appear in rawParameters")
		assert.NotContains(t, updRaw, "administrators", "merged-state fields from provisioning must not leak into update rawParameters")
	})
}

func TestSequentialUpdatesParametersIsolation(t *testing.T) {
	// Verifies that:
	// 1. instance-level parameters reflect the latest merged state after each update
	// 2. each update operation's rawParameters contains only the fields submitted in that update
	//    (no bleedthrough from provisioning or previous updates)

	// given
	cfg := fixConfig()
	suite := NewBrokerSuiteTestWithConfig(t, cfg)
	defer suite.TearDown()
	iid := uuid.New().String()

	// provision with autoScalerMin=3 on AWS plan (trial plan zeroes autoscaler values)
	provisionResp := suite.CallAPI("PUT", fmt.Sprintf("oauth/cf-eu21/v2/service_instances/%s?accepts_incomplete=true", iid),
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
				"autoScalerMin": 3
			}
		}`)
	defer func() { _ = provisionResp.Body.Close() }()
	provOpID := suite.DecodeOperationID(provisionResp)
	suite.processKIMProvisioningByOperationID(provOpID)
	suite.WaitForOperationState(provOpID, domain.Succeeded)

	// update 1: change only autoScalerMin to 5
	upd1Resp := suite.CallAPI("PATCH", fmt.Sprintf("oauth/cf-eu21/v2/service_instances/%s?accepts_incomplete=true&plan_id=361c511f-f939-4621-b228-d0fb79a1fe15&service_id=47c9dcbf-ff30-448e-ab36-d3bad66ba281", iid),
		`{
			"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
			"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
			"context": {
				"globalaccount_id": "g-account-id",
				"subaccount_id": "sub-id",
				"user_id": "john.smith@email.com"
			},
			"parameters": {
				"autoScalerMin": 5
			}
		}`)
	defer func() { _ = upd1Resp.Body.Close() }()
	require.Equal(t, http.StatusAccepted, upd1Resp.StatusCode)
	upd1OpID := suite.DecodeOperationID(upd1Resp)
	suite.WaitForOperationState(upd1OpID, domain.Succeeded)

	// update 2: change only autoScalerMax to 20 (different field than update 1)
	upd2Resp := suite.CallAPI("PATCH", fmt.Sprintf("oauth/cf-eu21/v2/service_instances/%s?accepts_incomplete=true&plan_id=361c511f-f939-4621-b228-d0fb79a1fe15&service_id=47c9dcbf-ff30-448e-ab36-d3bad66ba281", iid),
		`{
			"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
			"plan_id": "361c511f-f939-4621-b228-d0fb79a1fe15",
			"context": {
				"globalaccount_id": "g-account-id",
				"subaccount_id": "sub-id",
				"user_id": "john.smith@email.com"
			},
			"parameters": {
				"autoScalerMax": 20
			}
		}`)
	defer func() { _ = upd2Resp.Body.Close() }()
	require.Equal(t, http.StatusAccepted, upd2Resp.StatusCode)
	upd2OpID := suite.DecodeOperationID(upd2Resp)
	suite.WaitForOperationState(upd2OpID, domain.Succeeded)

	// when
	runtimesResp := suite.CallAPI("GET", fmt.Sprintf("runtimes?instance_id=%s&ops=all", iid), "")
	defer func() { _ = runtimesResp.Body.Close() }()
	require.Equal(t, http.StatusOK, runtimesResp.StatusCode)

	body, err := io.ReadAll(runtimesResp.Body)
	require.NoError(t, err)
	var page runtime.RuntimesPage
	require.NoError(t, json.Unmarshal(body, &page))
	require.Len(t, page.Data, 1)
	rt := page.Data[0]
	require.NotNil(t, rt.Status.Update)
	require.Len(t, rt.Status.Update.Data, 2)

	// operations are ordered most-recent first
	latestUpdate := rt.Status.Update.Data[0]
	firstUpdate := rt.Status.Update.Data[1]

	t.Run("instance-level parameters reflect latest merged state", func(t *testing.T) {
		// autoScalerMin set in update 1, autoScalerMax set in update 2 — both must be present
		require.NotNil(t, rt.Parameters.AutoScalerMin)
		assert.Equal(t, 5, *rt.Parameters.AutoScalerMin, "autoScalerMin should reflect update 1")
		require.NotNil(t, rt.Parameters.AutoScalerMax)
		assert.Equal(t, 20, *rt.Parameters.AutoScalerMax, "autoScalerMax should reflect update 2")
	})

	t.Run("update 1 rawParameters contains only autoScalerMin — no provisioning bleedthrough", func(t *testing.T) {
		var raw map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(firstUpdate.RawParameters, &raw))
		assert.Equal(t, json.RawMessage(`5`), raw["autoScalerMin"])
		assert.NotContains(t, raw, "name", "provisioning fields must not bleed into update rawParameters")
		assert.NotContains(t, raw, "region")
		assert.NotContains(t, raw, "autoScalerMax")
	})

	t.Run("update 2 rawParameters contains only autoScalerMax — no bleedthrough from update 1", func(t *testing.T) {
		var raw map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(latestUpdate.RawParameters, &raw))
		assert.Equal(t, json.RawMessage(`20`), raw["autoScalerMax"])
		assert.NotContains(t, raw, "autoScalerMin", "update 1 fields must not bleed into update 2 rawParameters")
		assert.NotContains(t, raw, "name")
	})
}

func TestProvisioningPayloadSizeLimit(t *testing.T) {
	// given
	cfg := fixConfig()
	suite := NewBrokerSuiteTestWithConfig(t, cfg)
	defer suite.TearDown()
	iid := uuid.New().String()

	// build a payload that exceeds 64 KB
	bigValue := make([]byte, 65*1024)
	for i := range bigValue {
		bigValue[i] = 'x'
	}
	oversizedParams := fmt.Sprintf(`{"name":"test","extra":"%s"}`, string(bigValue))
	body := fmt.Sprintf(`{
		"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
		"plan_id": "7d55d31d-35ae-4438-bf13-6ffdfa107d9f",
		"context": {
			"globalaccount_id": "g-account-id",
			"subaccount_id": "sub-id",
			"user_id": "john.smith@email.com"
		},
		"parameters": %s
	}`, oversizedParams)

	// when
	resp := suite.CallAPI("PUT", fmt.Sprintf("oauth/cf-eu10/v2/service_instances/%s?accepts_incomplete=true&plan_id=7d55d31d-35ae-4438-bf13-6ffdfa107d9f&service_id=47c9dcbf-ff30-448e-ab36-d3bad66ba281", iid), body)
	defer func() { _ = resp.Body.Close() }()

	// then
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestUpdatePayloadSizeLimit(t *testing.T) {
	// given
	cfg := fixConfig()
	suite := NewBrokerSuiteTestWithConfig(t, cfg)
	defer suite.TearDown()
	iid := uuid.New().String()

	provisionResp := suite.CallAPI("PUT", fmt.Sprintf("oauth/cf-eu10/v2/service_instances/%s?accepts_incomplete=true&plan_id=7d55d31d-35ae-4438-bf13-6ffdfa107d9f&service_id=47c9dcbf-ff30-448e-ab36-d3bad66ba281", iid),
		`{
			"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
			"plan_id": "7d55d31d-35ae-4438-bf13-6ffdfa107d9f",
			"context": {
				"sm_operator_credentials": {"clientid":"c","clientsecret":"s","url":"u","sm_url":"su"},
				"globalaccount_id": "g-account-id",
				"subaccount_id": "sub-id",
				"user_id": "john.smith@email.com"
			},
			"parameters": {"name": "testing-cluster"}
		}`)
	defer func() { _ = provisionResp.Body.Close() }()
	provOpID := suite.DecodeOperationID(provisionResp)
	suite.processKIMProvisioningByOperationID(provOpID)
	suite.WaitForOperationState(provOpID, domain.Succeeded)

	// oversized update payload
	bigValue := make([]byte, 65*1024)
	for i := range bigValue {
		bigValue[i] = 'x'
	}
	oversizedParams := fmt.Sprintf(`{"autoScalerMax":21,"extra":"%s"}`, string(bigValue))
	updateBody := fmt.Sprintf(`{
		"service_id": "47c9dcbf-ff30-448e-ab36-d3bad66ba281",
		"plan_id": "7d55d31d-35ae-4438-bf13-6ffdfa107d9f",
		"context": {
			"globalaccount_id": "g-account-id",
			"subaccount_id": "sub-id",
			"user_id": "john.smith@email.com"
		},
		"parameters": %s
	}`, oversizedParams)

	// when
	resp := suite.CallAPI("PATCH", fmt.Sprintf(updateRequestPathFormat, iid), updateBody)
	defer func() { _ = resp.Body.Close() }()

	// then
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
