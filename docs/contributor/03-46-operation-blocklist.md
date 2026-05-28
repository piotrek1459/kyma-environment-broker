<!--{"metadata":{"publish":false}}-->

# Operation Blocklist

## Overview

You can configure Kyma Environment Broker (KEB) to block specific operations (provisioning, deprovisioning, update, plan upgrade) for selected service plans. When a blocked operation is attempted, KEB rejects the request with an HTTP 400 error and the configured message.

## Configuration

The **APP_OPERATION_BLOCKLIST_FILE_PATH** environment variable points to the blocklist defined in a YAML file. In the Helm chart, set the **operationBlocklist** value.

```yaml
operationBlocklist: |-
  provision:
    - '"Provisioning of the trial plan is currently blocked","plan=trial"'
  deprovision:
    - '"Deprovisioning of the trial plan is currently blocked","plan=trial"'
```

The file is served from the existing `/config` volume through the `kcp-kyma-environment-broker` ConfigMap.

If you don't set **operationBlocklist** or leave it empty, no operations are blocked.

## Rule Format

Each rule is a compact string with quoted tokens separated by commas:

```
'"<message>"'
'"<message>","plan=<plan1>,<plan2>"'
'"<message>","plan=<plan1>,<plan2>","GA!=<globalAccountID>"'
'"<message>","GA!=<globalAccountID>"'
```

Use the following rule components:

- message — required, non-empty text string returned to the caller when the rule matches. Supports the `{plan}` placeholder, which is replaced with the actual plan name at runtime.
- plan filter — optional comma-separated list of plan names to match. When omitted together with all other filters, the rule is a no-op.
- `GA!=<globalAccountID>` — optional GlobalAccount exclusion. When present, the rule does **not** apply to operations from the specified GlobalAccount. All other GlobalAccounts are still blocked.

> **Note:** A rule with no filters at all (message only) is a no-op. At least one filter (`plan=` or `GA!=`) is required for a rule to take effect.

### Examples

```yaml
# Block provisioning for all plans (no filter → no-op)
provision: '"Provisioning is temporarily disabled"'
```

> ### Note:
> This rule has no filters and is therefore a no-op.

```yaml
# Block provisioning for trial only
provision: '"Provisioning of the {plan} plan is blocked","plan=trial"'

# Block update for multiple plans
update:
  - '"Updates are blocked for {plan}","plan=trial,free"'

# Block plan upgrade and deprovision for trial
planUpgrade: '"Plan upgrade is not allowed for {plan}","plan=trial"'
deprovision: '"Deprovisioning is blocked for {plan}","plan=trial"'

# Block trial provisioning for everyone except one GlobalAccount
provision: '"Trial plan temporarily suspended.","plan=trial","GA!=12234243534"'

# Block trial provisioning for all GlobalAccounts except two
provision:
  - '"Trial plan temporarily suspended.","plan=trial","GA!=11111111111"'
  - '"Trial plan temporarily suspended.","plan=trial","GA!=22222222222"'
```

> ### Note:
> Each `GA!=` token exempts exactly one GlobalAccount. To exempt multiple accounts, add one rule per account. All rules are always evaluated — a rule whose `GA!=` exclusion matches the current account is skipped and the next rule is checked. An account is blocked only if **all** rules match it (none of the `GA!=` exclusions apply).

## Supported Operations

| Key | Operation blocked |
|---|---|
| **provision** | New instance provisioning |
| **update** | Instance update |
| **planUpgrade** | Plan upgrade |
| **deprovision** | Instance deprovisioning |

## Validation

KEB validates the blocklist at startup. The following configurations are rejected with an error:

| Invalid input | Error |
|---|---|
| `'""'` | Empty message |
| `'"msg","plan="'` | Empty plan filter |
| `'"msg","plan=aws,,gcp"'` | Empty segment in plan list |
| `'"msg","GA!="'` | Empty GA value |
| `'"msg",'` | Trailing comma |
| Unknown top-level key (for example, `planUpgarde`) | Typo detection |
| Unknown plan name (for example, `trail`) | Caught by plan validator at startup |

A rule with no filters (`'"msg"'`) or an empty string rule (`''`) or an empty key (for example, `provision:`) is a no-op and does not cause an error.

> **Note:** `GA!=` values are **not** validated at startup (unlike plan names). An incorrect GlobalAccount ID results in the rule never matching its exclusion, effectively blocking all accounts for that plan.

## Plan Names

Valid plan names are the same as those enabled using **broker.enablePlans**, for example, `aws`, `azure`, `gcp`, `trial`, `free`. A typo in a plan name (for example, `trail` instead of `trial`) causes a startup error.

## Extending the Rule Format

The rule format is designed for extensibility. Future filters follow the same token pattern:

- Positive filter (`key=value`): rule applies only when the attribute matches
- Negation filter (`key!=value`): rule does not apply when the attribute matches

To add a SubAccount exclusion (`SA!=<subAccountID>`), extend `OperationContext` and `Rule` in `internal/blocklist/blocklist.go` following the existing `GA!=` pattern.
