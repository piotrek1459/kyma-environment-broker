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

### Tokens

**message** — required. Non-empty text returned to the caller when the rule matches. Supports the `{plan}` placeholder, which KEB replaces with the actual plan name at runtime.

**`plan=<plan1>,<plan2>`** — required when any GA filter is present. Comma-separated list of plan names. The rule matches only operations on one of the listed plans.

- A single plan: `plan=trial`
- Multiple plans: `plan=trial,aws` — matches both trial and aws

**`GA=<globalAccountID>`** — optional. The rule matches **only** the specified GlobalAccount. All other GlobalAccounts are not blocked by this rule.

**`GA!=<globalAccountID>`** — optional. The rule does **not** match the specified GlobalAccount. All other GlobalAccounts are blocked by this rule.

> **Note:** `GA=` and `GA!=` each accept a single GlobalAccount ID. To target multiple accounts, use multiple rules. See [Multiple Rules](#multiple-rules).

> **Note:** `GA=` and `GA!=` require `plan=` to be present. A rule with a GA filter but no plan filter is rejected at startup.

> **Note:** A rule with only a message and no filters is a no-op and does not cause an error.

### Filter Semantics

All filters in a single rule are combined with **AND** — a rule matches only when every filter condition is satisfied:

| `plan=` | `GA=` / `GA!=` | matches when |
|---|---|---|
| `plan=trial` | — | plan is trial |
| `plan=trial` | `GA=X` | plan is trial **and** GA is X |
| `plan=trial` | `GA!=X` | plan is trial **and** GA is not X |

`GA=` means "block only this GA" — the rule is a targeted block for one account.

`GA!=` means "block everyone except this GA" — the rule is a broad suspension with a single exemption.

## Multiple Rules

Rules within an operation type are evaluated in order. **The first matching rule wins** — evaluation stops and its message is returned. Rules that do not match are skipped.

This means:
- More specific rules (with `GA=`) should come **before** broader ones (with `plan=` only).
- `GA!=` with two different accounts in separate rules does **not** create two exemptions — see the pitfall below.

### Patterns

**Block specific accounts, allow everyone else:**

```yaml
provision:
  - '"Blocked for GA1","plan=trial","GA=ga-1"'
  - '"Blocked for GA2","plan=trial","GA=ga-2"'
```

| plan | GA | result |
|---|---|---|
| trial | `ga-1` | blocked — "Blocked for GA1" |
| trial | `ga-2` | blocked — "Blocked for GA2" |
| trial | anything else | allowed |

**Block everyone except one account (broad suspension with exemption):**

```yaml
provision:
  - '"Trial suspended","plan=trial","GA!=ga-exempt"'
```

| plan | GA | result |
|---|---|---|
| trial | `ga-exempt` | allowed |
| trial | anything else | blocked — "Trial suspended" |

**Catch-all after specific rules:**

```yaml
provision:
  - '"VIP account","plan=trial","GA=ga-vip"'   # blocks only ga-vip
  - '"Trial suspended for {plan}","plan=trial"' # catch-all for everyone else
```

| plan | GA | result |
|---|---|---|
| trial | `ga-vip` | blocked — "VIP account" (rule 1 matches) |
| trial | anything else | blocked — "Trial suspended for trial" (rule 1 doesn't match, rule 2 does) |
| aws | anything | allowed (neither rule matches) |

**`GA!=` pitfall — does NOT create multiple exemptions:**

```yaml
provision:
  - '"Trial suspended","plan=trial","GA!=ga-exempt-1"'
  - '"Trial suspended","plan=trial","GA!=ga-exempt-2"'
```

| plan | GA | result | why |
|---|---|---|---|
| trial | `ga-exempt-1` | **blocked** | rule 1 skips, rule 2 matches |
| trial | `ga-exempt-2` | **blocked** | rule 1 matches |
| trial | anything else | **blocked** | rule 1 matches |

To exempt multiple accounts use `GA=` (block specific accounts) instead of `GA!=`.

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
| `'"msg","GA=X"'` | GA filter without `plan=` |
| `'"msg","GA!=X"'` | GA filter without `plan=` |
| `'"msg",'` | Trailing comma |
| Unknown top-level key (for example, `planUpgarde`) | Typo detection |
| Unknown plan name (for example, `trail`) | Caught by plan validator at startup |

A rule with only a message (`'"msg"'`), an empty string rule (`''`), or an empty key (for example, `provision:`) is a no-op and does not cause an error.

> **Note:** `GA=` and `GA!=` values are **not** validated at startup (unlike plan names). An incorrect GlobalAccount ID in `GA!=` results in the rule never skipping anyone — everyone is blocked. An incorrect ID in `GA=` results in the rule never matching — no one is blocked by that rule.

## Plan Names

Valid plan names are the same as those enabled using **broker.enablePlans**, for example, `aws`, `azure`, `gcp`, `trial`, `free`. A typo in a plan name (for example, `trail` instead of `trial`) causes a startup error.

## Extending the Rule Format

The rule format is designed for extensibility. Future filters follow the same token pattern:

- Positive filter (`key=value`): rule applies only when the attribute matches
- Negation filter (`key!=value`): rule does not apply when the attribute matches

To add a SubAccount filter (`SA=` / `SA!=<subAccountID>`), extend `OperationContext` and `Rule` in `internal/blocklist/blocklist.go` following the existing `GA=` / `GA!=` pattern.
