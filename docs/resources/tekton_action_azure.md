# facets_tekton_action_azure

Creates a Tekton Task and StepAction for Azure-based workflows, with Azure credentials injected automatically.

## How It Works

The resource creates two objects in the `tekton-pipelines` namespace:

1. A **StepAction** named `setup-credentials-{hash}`, which runs `az login` using the credentials from the provider's `azure` block and writes the resulting token cache to `/workspace/.azure`.
2. A **Task** named `{hash}`, whose first step references that StepAction. Every user-defined step that follows receives `AZURE_CONFIG_DIR=/workspace/.azure`, plus `AZURE_SUBSCRIPTION_ID`, `AZURE_TENANT_ID` and `AZURE_CLIENT_ID`.

Your step scripts therefore call `az` directly and never handle credentials.

TaskRuns execute under the `facets-workflows-sa` ServiceAccount in the `tekton-pipelines` namespace.

Tasks and StepActions are labelled `cloud_action=true`, so running them requires the `RUN_CLOUD_ACTION` permission in Facets rather than plain `RUN_ACTION`.

## Authentication

Two mutually exclusive methods. **Prefer workload identity** — it is the Azure analogue of the IRSA-only stance the AWS action takes, and it stores no secret anywhere.

### Workload identity (recommended)

The `facets-workflows-sa` ServiceAccount is federated to an Azure AD application. The Workload Identity webhook projects a short-lived token into the pod, which `az login --federated-token` exchanges for an access token.

Prerequisites:

- The [Azure Workload Identity webhook](https://azure.github.io/azure-workload-identity/) installed in the cluster.
- A federated credential on the Azure AD application trusting the cluster's OIDC issuer with subject `system:serviceaccount:tekton-pipelines:facets-workflows-sa`.
- The `facets-workflows-sa` ServiceAccount labelled `azure.workload.identity/use: "true"` and annotated `azure.workload.identity/client-id: <client-id>`.

This works on AKS and on any cluster with a publicly reachable OIDC issuer — including EKS — because Azure federated credentials accept any OIDC provider.

### Client secret reference

Where workload identity is not available, point the provider at an **existing** Kubernetes Secret holding the service principal password:

```bash
kubectl create secret generic facets-azure-sp \
  -n tekton-pipelines \
  --from-literal=client-secret='<sp-password>'
```

The provider never reads the value. It emits a `secretKeyRef` into the StepAction, so the password stays out of Terraform state and out of the StepAction object in-cluster.

There is deliberately **no inline client-secret argument**. Passing the password through Terraform would write it in plaintext into both the state file and the cluster object.

## Provider Configuration

```hcl
# Workload identity — no stored secret
provider "facets" {
  azure = {
    subscription_id       = "00000000-0000-0000-0000-000000000000"
    tenant_id             = "11111111-1111-1111-1111-111111111111"
    client_id             = "22222222-2222-2222-2222-222222222222"
    use_workload_identity = true
  }
}
```

```hcl
# Service principal password from an existing Kubernetes Secret
provider "facets" {
  azure = {
    subscription_id = "00000000-0000-0000-0000-000000000000"
    tenant_id       = "11111111-1111-1111-1111-111111111111"
    client_id       = "22222222-2222-2222-2222-222222222222"

    client_secret_ref = {
      secret_name = "facets-azure-sp"
      secret_key  = "client-secret" # optional, this is the default
    }
  }
}
```

## Environment Variables

Injected into every user-defined step:

| Variable | Value |
|---|---|
| `AZURE_CONFIG_DIR` | `/workspace/.azure` |
| `AZURE_SUBSCRIPTION_ID` | from provider config |
| `AZURE_TENANT_ID` | from provider config |
| `AZURE_CLIENT_ID` | from provider config |

`AZURE_CLIENT_SECRET` is scoped to the `setup-credentials` step and is **not** propagated to user steps.

The provider also reads `CLUSTER_ID` from its own environment for the `cluster_id` label, defaulting to `na`.

## Example Usage

### Start and stop an Azure Database for PostgreSQL Flexible Server

```hcl
resource "facets_tekton_action_azure" "stop_database" {
  name        = "stop-database"
  description = "Stops the PostgreSQL Flexible Server to save compute cost"

  facets_resource_name = var.instance_name
  facets_environment   = var.environment
  facets_resource      = var.instance

  steps = [{
    name  = "stop-flexible-server"
    image = "mcr.microsoft.com/azure-cli:2.89.1"
    env = [
      {
        name  = "RESOURCE_GROUP"
        value = var.inputs.resource_group.attributes.name
      },
      {
        name  = "SERVER_NAME"
        value = local.server_name
      },
    ]
    script = <<-EOT
      #!/bin/bash
      set -e

      STATE=$(az postgres flexible-server show \
        --resource-group "$RESOURCE_GROUP" \
        --name "$SERVER_NAME" \
        --query state -o tsv)

      echo "Current state: $STATE"

      if [ "$STATE" = "Stopped" ]; then
        echo "Server is already stopped"
        exit 0
      fi

      if [ "$STATE" != "Ready" ]; then
        echo "Cannot stop server from state '$STATE'" >&2
        exit 1
      fi

      az postgres flexible-server stop \
        --resource-group "$RESOURCE_GROUP" \
        --name "$SERVER_NAME"

      echo "Stop issued. Azure auto-starts a stopped server after 7 days."
    EOT
  }]
}
```

### With compute limits and a parameter

```hcl
resource "facets_tekton_action_azure" "scale_aks_nodepool" {
  name                 = "scale-nodepool"
  description          = "Scales an AKS node pool to a given node count"
  facets_resource_name = var.instance_name
  facets_environment   = var.environment
  facets_resource      = var.instance

  params = [{
    name = "NODE_COUNT"
    type = "string"
  }]

  steps = [{
    name  = "scale"
    image = "mcr.microsoft.com/azure-cli:2.89.1"
    resources = {
      requests = { cpu = "100m", memory = "256Mi" }
      limits   = { cpu = "500m", memory = "512Mi" }
    }
    env = [{
      name  = "NODE_COUNT"
      value = "$(params.NODE_COUNT)"
    }]
    script = <<-EOT
      #!/bin/bash
      set -e
      az aks nodepool scale \
        --resource-group "${var.resource_group}" \
        --cluster-name "${var.cluster_name}" \
        --name workers \
        --node-count "$NODE_COUNT"
    EOT
  }]
}
```

## Argument Reference

### Required Arguments

- `name` — Display name of the Tekton Task (1–253 characters).
- `facets_resource_name` — Resource name as defined in the Facets blueprint.
- `facets_environment` — Object with `unique_name`.
- `facets_resource` — Object with `kind`. `flavor`, `version` and `spec` may be supplied but are ignored.
- `steps` — List of step objects; each requires `name`, `image` and `script`.

### Optional Arguments

- `description` — Description of the Tekton Task. Defaults to the generated task name.
- `params` — List of `{ name, type }` objects. `type` is one of `string`, `array`, `object`.

### Optional Step Arguments

- `resources` — `{ requests, limits }`, each a map of compute resources.
- `env` — List of `{ name, value }`. Names must match `^[A-Z_][A-Z0-9_]*$`.

## Attribute Reference

- `id` — `tekton-pipelines/{task_name}`.
- `task_name` — Generated Task name: an MD5 hash of `resource_name-environment-name`, truncated to 63 characters.
- `step_action_name` — Generated StepAction name, `setup-credentials-{hash}`.

## Import

```bash
terraform import facets_tekton_action_azure.example 59f6f855860ddc99a32e2944c96db5fa
# or
terraform import facets_tekton_action_azure.example tekton-pipelines/59f6f855860ddc99a32e2944c96db5fa
```

Import is partial: `facets_environment`, `facets_resource`, `steps` and `params` cannot be reconstructed from the Task and must be specified in configuration.

## Limitations

- **Token lifetime.** The `setup-credentials` step logs in once and later steps reuse the cached token. Tasks running longer than the Azure AD access-token lifetime (typically one hour) may see the token expire mid-run. Under workload identity a step can re-login itself, because the federated token file is projected into every container; under `client_secret_ref` it cannot, since the password is not propagated past the setup step. Split very long actions into separate TaskRuns.
- **One subscription per provider block.** Actions targeting a second subscription need a second aliased provider configuration.

## Troubleshooting

### Check TaskRun logs

```bash
kubectl get taskruns -n tekton-pipelines -l resource_name=<blueprint-resource>
kubectl logs -n tekton-pipelines <pod> -c step-setup-credentials
```

### Common issues

| Symptom | Cause |
|---|---|
| `AZURE_FEDERATED_TOKEN_FILE is not set` | Workload Identity webhook missing, or `facets-workflows-sa` lacks the `azure.workload.identity/use=true` label |
| `AADSTS70021: No matching federated identity record found` | The federated credential's subject does not match `system:serviceaccount:tekton-pipelines:facets-workflows-sa`, or the issuer URL is wrong |
| `AZURE_CLIENT_SECRET is empty` | The referenced Secret does not exist in `tekton-pipelines`, or the key name differs from `secret_key` |
| `AuthorizationFailed` on an `az` call | The service principal lacks an Azure RBAC role assignment on the target scope |

### Debug commands

```bash
# Confirm the identity the task authenticated as
az account show

# Confirm the token cache landed where later steps expect it
ls -la "$AZURE_CONFIG_DIR"
```
