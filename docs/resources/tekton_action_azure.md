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

## Environment variables available to your steps

| Variable | Value |
|---|---|
| `AZURE_CONFIG_DIR` | `/workspace/.azure` — Azure CLI profile written by `setup-credentials` |

Because the CLI is already authenticated, steps can call `az ...` directly.

## Note on `params`

Custom params are **not** exposed as shell environment variables. Reference them with
Tekton's own syntax, e.g. `$(params.MY_PARAM)`.
