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

### OIDC federation (preferred)

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

> **Status: requires a control-plane change.** Verified against a live action run:
> the control plane creates TaskRuns with a fixed `serviceAccountName`
> (`facets-actions-sa`) and `podTemplate: null`, so an action cannot request its
> own projected volume. The pod *does* already receive a projected token — the
> EKS pod-identity webhook injects one because that ServiceAccount carries an
> `eks.amazonaws.com/role-arn` annotation — but its audience is
> `sts.amazonaws.com`, not `api://AzureADTokenExchange`, so Entra rejects it with
> `AADSTS700212`.
>
> Two possible fixes, both control-plane side:
> 1. Add a second projected token with audience `api://AzureADTokenExchange` to
>    the TaskRun pod template (additive; does not disturb the existing AWS token).
> 2. Let the action declare a service account / pod template, so an
>    Azure-federated ServiceAccount can be used.
>
> Until then, use **client secret** mode, which needs no control-plane change.

### Client secret

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

The secret is read at pod start from the `facets-azure-credentials` Kubernetes
Secret via `secretKeyRef`, so it does not appear in the rendered `Task` manifest.
Note that a secret supplied here **is** persisted in Terraform state — prefer OIDC
federation wherever the federated credential can be registered.

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
