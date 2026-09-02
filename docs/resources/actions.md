# facets_actions

Manages a Tekton Task for a Facets action, for any cloud or none.

## How it works

The resource creates a single Tekton **Task** in the `tekton-pipelines` namespace (or one you choose). There is no StepAction — see [Why no StepAction](#why-no-stepaction).

Cloud credentials are never configured on the provider and never appear in the rendered Task. The provider collects them, writes them to a Kubernetes Secret, and attaches that Secret to every step with `envFrom`. Each key arrives in the pod as an environment variable under its own name, so the module's script performs whatever login its cloud requires.

```
credential source  ──►  provider  ──►  Secret  ──►  envFrom  ──►  step env
```

The Task carries only a `secretRef`, so no credential is readable by anyone with `kubectl` on the namespace. Whether the value also stays out of **Terraform state** depends on which of the three sources below supplies it.

## Why the source matters

Terraform persists module outputs and resource attributes to state in plain text. `sensitive = true` only redacts CLI output — [it does not keep a value out of the state file](https://developer.hashicorp.com/terraform/language/state/sensitive-data). So any credential routed through HCL, including one arriving via a module output, ends up in state.

A credential supplied through the provider's own environment (source 2) is never observed by Terraform, so there is nothing for it to persist. One set on the `credentials` attribute (source 1) does.

## Supplying credentials

Two sources, in precedence order.

| # | Source | Credential in state | Status |
|---|---|---|---|
| 1 | `credentials` — a map on the resource, set by the module | **Yes** | Verified |
| 2 | `FACETS_ACTION_CRED_*` in the provider's environment | No | **Prerequisite, see below** |

Either way the provider writes the values to a Kubernetes Secret owned by this action alone, named `facets-action-creds-<task_name>`, and attaches it to every step with `envFrom`. Only the name reaches Terraform state.

The Secret is per action rather than per environment, so two modules in one environment can hold different credentials without overwriting each other, and the Secret is deleted with the action that owns it.

Credentials are meant to be supplied by the **module**, not by whoever configures the resource. A module derives them from its own `cloud_account` input, so nothing credential-shaped appears in the blueprint spec or the UI form.

### 1. Values from the module

```hcl
credentials = {
  AZURE_CLIENT_ID     = var.inputs.cloud_account.attributes.client_id
  AZURE_CLIENT_SECRET = var.inputs.cloud_account.attributes.client_secret
}
```

The module owns the mapping, so an AWS module emits `AWS_*` from its own cloud account and a GCP one emits `GOOGLE_*`. The user configures nothing. The values do **not** appear in the rendered Task, which carries only a `secretRef`, but they **are** written to Terraform state: a resource attribute always is, before Terraform 1.11's write-only arguments, and `sensitive = true` only redacts CLI output. On Terraform 1.11+ this attribute should become write-only, which removes that exposure without changing any module.

### 2. The provider's environment

> [!IMPORTANT]
> **Prerequisite: the Terraform runner must already export these variables, and this path has not been exercised end to end.**
>
> On a Facets control plane the runner's environment is a fixed set. Arbitrary variables reach it only through cluster-scoped Terraform run configuration (`additionalEnvVars`), which is control-plane administration and is not exposed by the `raptor` CLI. Without that, no `FACETS_ACTION_CRED_*` variable is present and the provider falls back to the `credentials` attribute.
>
> The code path is unit-tested; it has not been run against a live control plane. Treat it as the intended end state rather than a supported option today.

Export any variable prefixed `FACETS_ACTION_CRED_` to the process running Terraform. The prefix is stripped:

```bash
FACETS_ACTION_CRED_AWS_ACCESS_KEY_ID=AKIA...       →  AWS_ACCESS_KEY_ID
FACETS_ACTION_CRED_AWS_SECRET_ACCESS_KEY=...       →  AWS_SECRET_ACCESS_KEY
FACETS_ACTION_CRED_AZURE_CLIENT_SECRET=...         →  AZURE_CLIENT_SECRET
FACETS_ACTION_CRED_GOOGLE_APPLICATION_CREDENTIALS=/creds.json
```

The provider does not interpret them. AWS static keys, an assumed-role session, an Azure service principal, Azure managed identity settings and a GCP service account all travel the same path.

Names must be valid C identifiers — letters, digits and underscore, not starting with a digit. Kubernetes injects Secret keys verbatim as variable names and silently skips anything else, reporting it only as a pod event, so the provider rejects such names at apply time instead.

Credentials are scoped to an environment, so every action in one environment shares a single Secret. One credential, one rotation.

## Rotation

Under source 2 the credential appears in no attribute, so nothing changes when it rotates and Terraform would never call `Update`. The resource therefore reconciles the Secret during `Read` as well.

The reconcile compares before writing, so a plan against unchanged credentials performs no write and needs no update permission. When the values differ, the next plan or apply converges them.

The comparison is exact: a key stored in the Secret that the module no longer supplies counts as a mismatch and is removed. Otherwise a credential that had been revoked upstream would keep being injected through `envFrom` indefinitely.

## Example

```hcl
resource "facets_actions" "stop_database" {
  name         = "Stop Database"
  description  = "Stops the database to save compute cost outside business hours"
  cloud_action = true

  facets_resource_name = var.instance_name
  facets_environment   = { unique_name = var.environment.unique_name }
  facets_resource      = { kind = "postgres" }

  steps = [{
    name  = "stop"
    image = "mcr.microsoft.com/azure-cli:2.89.1"

    env = {
      RESOURCE_GROUP = var.inputs.resource_group.attributes.name
      SERVER_NAME    = local.server_name
    }

    script = <<-EOT
      #!/bin/bash
      set -e

      # AZURE_* variables arrive from the credentials Secret via envFrom.
      az login --service-principal \
        --username "$AZURE_CLIENT_ID" \
        --password "$AZURE_CLIENT_SECRET" \
        --tenant   "$AZURE_TENANT_ID" \
        --output none

      az postgres flexible-server stop \
        --resource-group "$RESOURCE_GROUP" \
        --name "$SERVER_NAME"
    EOT
  }]
}
```

The same resource with an AWS step differs only in the image and the script.

## Why no StepAction

The per-cloud resources prepend a StepAction that injects credentials. That approach cannot be made cloud-agnostic:

- Tekton rejects `env` on a step carrying a `ref` — *"env cannot be used with Ref"*.
- `StepActionSpec` has no `envFrom` field at all, so a Secret's keys cannot be injected wholesale.

Together those mean a StepAction can only carry credentials whose names the provider already knows, which is the per-cloud coupling this resource removes. Dropping it also leaves one object to manage instead of two — no create-rollback, no two-object drift detection, no partial-failure states.

## Argument reference

### Required

- `name` — display name of the action.
- `facets_resource_name` — resource name from the blueprint.
- `facets_environment` — object with `unique_name`.
- `facets_resource` — object with `kind`.
- `steps` — list of steps; each needs `name`, `image` and `script`.

### Optional

- `description` — what the action does.
- `cloud_action` — whether the action mutates cloud infrastructure. Sets the `cloud_action` label, which decides whether Facets requires `RUN_CLOUD_ACTION` rather than `RUN_ACTION`. **Set it `true` for anything changing state outside the cluster**; the default of `false` grants the weaker permission.
- `namespace` — defaults to `tekton-pipelines`. Changing it forces replacement.
- `labels` — extra labels, merged with the generated ones, which win on conflict. Keys and values are sanitized to what Kubernetes accepts; see [Label sanitization](#label-sanitization).
- `params` — list of `{ name, type }`; type is `string`, `array` or `object`.

### Optional step arguments

- `env` — non-sensitive variables as `name => value`, emitted in sorted key order. **These appear verbatim in the Task**, so never put a credential here.
- `resources` — `{ requests, limits }` compute maps.

## Attribute reference

- `id` — `<namespace>/<task_name>`.
- `task_name` — generated Task name, a hash of resource name, environment and action name.
- `credentials_secret` — name of the Secret the provider maintains for this environment. The name only; never the values.

## Label sanitization

Generated labels — `display_name`, `resource_name`, `resource_kind`, `environment_unique_name`, `cluster_id` — carry human-authored strings from a blueprint, and Kubernetes rejects most of what people type. An action called `Stop Database` used to fail the entire apply with `metadata.labels: Invalid value`. Values are now coerced to at most 63 characters of `[A-Za-z0-9._-]`, beginning and ending alphanumeric; user-supplied label keys and values are coerced too.

Two properties are worth relying on:

**A value Kubernetes already accepts is returned untouched.** Sanitization only transforms input that would otherwise fail. This matters because the same code runs for `facets_tekton_action_aws` and `facets_tekton_action_kubernetes`, so every action that works today keeps the identical label — no in-place Task update, and nothing that resolves an action by `display_name` starts missing. `restart--db` stays `restart--db`.

**Distinct names never collapse onto one label.** A value that strips to nothing — a name in a non-Latin script, or pure punctuation — or one exceeding 63 characters falls back to a digest suffix rather than to `""` or a bare truncation.

The transformation is lossy, so the label is not a way to recover the original name. `name` is deliberately not reconstructed from it on import for that reason.

## Import

```bash
terraform import facets_actions.example <task-name>
terraform import facets_actions.example tekton-pipelines/<task-name>
```

Import is partial. `name` is deliberately not reconstructed from the `display_name` label: that label is sanitized for Kubernetes and cannot round-trip a name containing a space or any other rejected character, and importing a silently wrong value is worse than leaving it to be supplied.

## Notes

- **The credentials Secret outlives individual actions.** It is shared across an environment, so deleting one action does not remove it. It is reclaimed with the environment.
- **One credential set per environment.** If actions in a single environment need credentials for two different accounts, they cannot both come from one runner environment today.
