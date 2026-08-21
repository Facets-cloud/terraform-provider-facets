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

## Authentication modes

There is no `mode` argument. The mode follows from **which fields you set**, checked
in this order:

| Fields set in the `azure` block | Mode |
|---|---|
| `client_secret` (with `subscription_id`, `tenant_id`, `client_id`) | **Client secret** — the supported mode |
| `use_oidc_federation = true` (with the three ids, no `client_secret`) | OIDC federation |
| `cloud_account_id` alone | Secrets Manager |
| none of the above | **error** — the apply fails, no mode is assumed |

The modes are mutually exclusive and the provider rejects combinations rather than
guessing: `cloud_account_id` with either of the others, or `client_secret` together
with `use_oidc_federation`, is an error naming the conflict.

Because these fields can be populated by an output-type mapping on a `cloud_account`
module -- and would then apply to every project using that account -- a mode is also
checked for feasibility at apply time, not just for consistency:

| Mistake | When it surfaces |
|---|---|
| `client_secret` + `use_oidc_federation` | apply — conflicting modes |
| `client_secret` + `cloud_account_id` | apply — conflicting modes |
| `use_oidc_federation` alone, cluster not set up for it | apply — cluster cannot issue a token with the exchange audience |
| `cloud_account_id` alone, no control-plane env | apply — the secret id cannot be derived |

The point of the third row is that a lone `use_oidc_federation = true` looks like a
deliberate choice. Without the check it applies cleanly and fails later with
`federated token not found`, seen first by whoever clicks the action rather than
whoever configured it.

> **Which one to use:** set `client_secret`. That is the mode this resource is
> deployed with today, and the only one exercised end to end against a live control
> plane. OIDC federation is the intended end state — it removes the standing secret
> entirely — but it needs a federated credential registered in Entra and a Tekton
> `config-defaults` change first. Secrets Manager mode works but is not in use.

### Client secret (the supported mode)

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

Nothing else to configure — see [How the Secret is named](#how-the-secret-is-named)
below for what the provider does on your behalf.

### Secret manager

The action resolves the credentials at run time from the control plane's secret
store, using the pod's own cloud identity (IRSA on an EKS-hosted control plane).
The blueprint holds only a cloud-account id.

```hcl
provider "facets" {
  azure = {
    cloud_account_id = "6851457022e37005a59327d0"
  }
}
```

A cloud-account id is the only input. The secret id is derived at apply time from
the control plane's own environment (`TF_VAR_CP_NAME` / `TF_VAR_CP_CLOUD`), using
the same convention as `cloudaccount-fetch-secret/secret-fetcher.py` — so **end
users never need to know the internal secret layout**. `secret_manager_path` is
an optional override for non-standard locations.

Note the derivation happens at apply time, not run time: the action pod does not
carry `TF_VAR_CP_NAME` (only the release pod does), so the resolved id is baked
into the generated step.

Nothing sensitive is stored in Terraform state, in the Tekton `Task` manifest, or
in a Kubernetes Secret — and there is **no per-cluster setup**. This reuses the
same secret layout the `cloud_account` modules already read via
`secret-fetcher.py`.

Two steps are generated: a fetch step on `facetscloud/actions-base-image` (it
needs the `aws` CLI, which the azure-cli image lacks) writes the credentials to
the shared `/workspace/.azure`, then an `az login` step on the azure-cli image
consumes and shreds them. User steps run already authenticated.

Requires the action pod's service account to be authorised for
`secretsmanager:GetSecretValue` — which the Facets release-pod IRSA role already
grants.

### OIDC federation

Microsoft Entra ID exchanges the pod's projected service-account token for an Azure
token, so **no client secret exists anywhere**. This is the Azure analogue of AWS
IRSA and works cross-cloud — a pod running in an EKS control-plane cluster can
authenticate to Azure.

```hcl
provider "facets" {
  azure = {
    subscription_id     = "00000000-0000-0000-0000-000000000000"
    tenant_id           = "11111111-1111-1111-1111-111111111111"
    client_id           = "22222222-2222-2222-2222-222222222222"
    use_oidc_federation = true
  }
}
```

Prerequisites:

1. A **federated identity credential** on the app registration, trusting the
   cluster's OIDC issuer with subject
   `system:serviceaccount:<namespace>:<serviceaccount>` and audience
   `api://AzureADTokenExchange`.
2. The TaskRun pod must mount a projected service-account token with that **same
   audience**. Reusing the default service-account token fails with
   `AADSTS700212` (audience mismatch).

For AKS, enable `oidc_issuer_enabled` and `workload_identity_enabled` on the
cluster. For EKS/GKE, the cluster's OIDC issuer URL is used directly — Entra ID
accepts any OIDC issuer.

> **Status: not yet usable — needs cluster setup, but no control-plane code change.**
>
> The pod already receives a projected token (the EKS pod-identity webhook injects
> one because `facets-actions-sa` carries an `eks.amazonaws.com/role-arn`
> annotation), but its audience is `sts.amazonaws.com`, so Entra rejects it with
> `AADSTS700212`.
>
> Two prerequisites, neither in application code:
> 1. A federated identity credential on the app registration. Without it Entra
>    returns `AADSTS70025`; with a wrong audience, `AADSTS700212`. Both were
>    observed, confirming Entra accepts the token's issuer.
> 2. A second projected token with audience `api://AzureADTokenExchange`. Tekton's
>    `config-defaults` ConfigMap supports `default-pod-template`, which is
>    currently unset — so this is a ConfigMap key, additive, leaving the existing
>    AWS token untouched.
>
> An earlier revision of this page claimed a control-plane code change was needed.
> That was wrong: `TektonTaskService.java:342-378` sets only name, annotations,
> taskRef and params — never `serviceAccountName` or `podTemplate`.
> `facets-actions-sa` comes from the ConfigMap above.

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

Setting both `client_secret` and `use_oidc_federation`, or neither, is rejected.

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
