# facets_tekton_action_azure

Manages a Tekton `Task` and `StepAction` for Azure-based workflows.

Credentials are configured **once** at the provider level and injected automatically
by a prepended `setup-credentials` step. The user triggering the action in the Facets
UI never supplies credentials — the same one-click experience
`facets_tekton_action_aws` provides via IRSA.

## Why not pass credentials as `params`?

Because it is both worse UX and worse security hygiene. Nothing in this provider is
marked `Sensitive` and step `env` accepts only literal values, so a credential
interpolated into `script` or `env` is persisted in **both** the Terraform state and
the in-cluster Tekton `Task` manifest. Declaring credentials as `params` avoids that,
but then a human has to paste a client secret on every single run. This resource
removes the trade-off.

## Authentication

Supply the service principal that owns the target resources:

```hcl
provider "facets" {
  azure = {
    subscription_id = "..."
    tenant_id       = "..."
    client_id       = "..."
    client_secret   = var.client_secret
  }
}
```

That is the whole configuration. All four fields are required, and there is no mode
to choose: the provider creates and maintains the Kubernetes Secret holding the
password, and the action reads it via `secretKeyRef` at pod start.

In a Facets blueprint this block is generated from the `cloud_account` module's
output type rather than hand-written.

> **On the modes that used to be here.** Earlier revisions also supported OIDC
> federation and resolving credentials from the control plane's secret manager,
> selected by the *presence* of a `use_oidc_federation` or `cloud_account_id` field.
> Both were removed deliberately.
>
> A mode chosen by the presence of a field can be switched by accident. These fields
> can be populated by an output-type mapping on a `cloud_account` module, so a single
> mistake there would change authentication for **every project using that account**.
> Worse, a lone `use_oidc_federation = true` was indistinguishable from a deliberate
> choice: it applied cleanly and failed only when a user clicked the action, with
> `federated token not found` — surfacing to the wrong person at the wrong time.
>
> Removing the fields makes that class of failure impossible: Terraform now rejects
> them as `Unsupported argument` before any provider code runs. The implementations
> remain in git history (`344e041`) if OIDC federation is revisited, which is still
> the better end state — it removes the standing secret entirely, and needs a
> federated credential in Entra plus a Tekton `config-defaults` change, not a
> control-plane code change.

### How the Secret is named

Applies to client-secret mode.

The provider creates and maintains the Kubernetes Secret that backs it, and the
action reads it at pod start via `secretKeyRef`, so the password never appears in the
rendered `Task` or `StepAction` manifest. Provider configuration is not written to
Terraform state.

The name is derived from the service principal identity:

```
facets-azure-creds-<sha256(tenant_id|client_id|subscription_id)[:16]>
```

This is deliberate rather than configurable-by-default, because the two parties
involved cannot see each other. The Secret lives in the control plane's namespace,
while the module referencing it is configured by someone with no access to that
namespace — so a name typed in one place cannot be looked up in the other. Deriving
it from three values both sides already hold removes the coordination entirely.

It also makes multi-account control planes safe:

| Situation | Result |
|---|---|
| Several actions, same service principal | One derived name → one shared Secret. Applies are idempotent. |
| Different service principals | Different names, so no project can overwrite another's credentials. |
| Credential rotated | Same name, value rewritten in place; every action on that SP picks it up on its next run. |

Because a Secret may be shared, it carries no `ownerReferences` — deleting one
action must not garbage-collect credentials another action still uses. The Secret is
inert without an action referencing it. It is labelled
`app.kubernetes.io/managed-by=terraform-provider-facets` and annotated with the
tenant, client and subscription ids, so the hashed name remains traceable to an
identity by inspection.

`secret_name` and `secret_key` may still be set in the `azure` block to pin a
specific name — useful when adopting an existing Secret — but neither is required.

## Argument reference

Identical to [`facets_tekton_action_aws`](tekton_action_aws.md):

* `name` (String, Required) — display name of the Tekton Task
* `description` (String, Optional)
* `facets_resource_name` (String, Required) — blueprint resource name
* `facets_environment` (Object, Required) — `{ unique_name }`
* `facets_resource` (Object, Required) — `{ kind }`
* `steps` (List, Required) — `name`, `image`, `script`, optional `resources`, `env`
* `params` (List, Optional) — custom Tekton params. **Not needed for credentials.**

### Attribute reference

* `id` — `namespace/task_name`
* `task_name` — generated Tekton Task name (hash-based, max 63 chars)
* `step_action_name` — generated StepAction name for credential setup

## Step image

The credential-setup StepAction uses `mcr.microsoft.com/azure-cli:2.61.0` rather than
`facetscloud/actions-base-image:v1.0.0`, which the AWS and Kubernetes variants use.

That base image is Alpine 3.19 with `bash`, `curl`, `jq`, `python3`, `awscli`,
`kubectl`, `yq` and `git` — it has **no `az` CLI**, so `az login` would fail there.
If `az` is added to the base image, `AzureSetupImage` in
`internal/provider/tekton/stepaction_azure.go` can be pointed back at it so all
three action types share one image.

## Environment variables available to your steps

| Variable | Value |
|---|---|
| `AZURE_CONFIG_DIR` | `/workspace/.azure` — Azure CLI profile written by `setup-credentials` |

Because the CLI is already authenticated, steps can call `az ...` directly.

## Note on `params`

Custom params are **not** exposed as shell environment variables. Reference them with
Tekton's own syntax, e.g. `$(params.MY_PARAM)`.
