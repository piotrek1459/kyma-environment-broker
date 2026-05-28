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
'"<message>","plan=<plan1>,<plan2>","GA=<globalAccountID>"'
'"<message>","plan=<plan1>,<plan2>","GA!=<globalAccountID>"'
```

Use the following rule components:

- message — required, non-empty text string returned to the caller when the rule matches. Supports the `{plan}` placeholder, which is replaced with the actual plan name at runtime.
- `plan=` — required when using any GA filter. Comma-separated list of plan names to match. A rule with no filters at all (message only) is a no-op.
- `GA=<globalAccountID>` — optional GlobalAccount inclusion. When present, the rule applies **only** to the specified GlobalAccount. All other GlobalAccounts are not blocked by this rule.
- `GA!=<globalAccountID>` — optional GlobalAccount exclusion. When present, the rule does **not** apply to the specified GlobalAccount. All other GlobalAccounts are still blocked.

> **Note:** `GA=` and `GA!=` require `plan=` to be present. A rule with a GA filter but no plan filter is rejected at startup.

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

# Block trial provisioning for one specific GlobalAccount only
provision: '"Trial plan temporarily suspended.","plan=trial","GA=12234243534"'

# Block trial provisioning for two specific GlobalAccounts (use GA= and multiple rules)
provision:
  - '"Trial plan temporarily suspended.","plan=trial","GA=11111111111"'
  - '"Trial plan temporarily suspended.","plan=trial","GA=22222222222"'
```

> ### Note:
> `GA!=` exempts exactly one GlobalAccount — adding a second rule with a different `GA!=` does **not** exempt that account, because first-match-wins means the first rule still blocks it. To block a specific set of accounts, use `GA=` with one rule per account instead.

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
| `'"msg","GA="'` | Empty GA value |
| `'"msg","GA!="'` | Empty GA value |
| `'"msg","GA=X"'` | GA filter without plan= |
| `'"msg","GA!=X"'` | GA filter without plan= |
| `'"msg",'` | Trailing comma |
| Unknown top-level key (for example, `planUpgarde`) | Typo detection |
| Unknown plan name (for example, `trail`) | Caught by plan validator at startup |

A rule with no filters (`'"msg"'`) or an empty string rule (`''`) or an empty key (for example, `provision:`) is a no-op and does not cause an error.

> **Note:** `GA=` and `GA!=` values are **not** validated at startup (unlike plan names). An incorrect GlobalAccount ID in `GA!=` results in the rule never skipping anyone, effectively blocking all accounts. An incorrect ID in `GA=` results in the rule never matching, effectively blocking no one.

## Plan Names

Valid plan names are the same as those enabled using **broker.enablePlans**, for example, `aws`, `azure`, `gcp`, `trial`, `free`. A typo in a plan name (for example, `trail` instead of `trial`) causes a startup error.

## Extending the Rule Format

The rule format is designed for extensibility. Future filters follow the same token pattern:

- Positive filter (`key=value`): rule applies only when the attribute matches
- Negation filter (`key!=value`): rule does not apply when the attribute matches

To add a SubAccount filter (`SA=` / `SA!=<subAccountID>`), extend `OperationContext` and `Rule` in `internal/blocklist/blocklist.go` following the existing `GA=` / `GA!=` pattern.
