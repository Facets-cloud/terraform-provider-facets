terraform {
  required_providers {
    facets = {
      source  = "Facets-cloud/facets"
      version = "~> 1.3"
    }
  }
}

# Workload identity: no secret is stored anywhere. The facets-workflows-sa
# service account in tekton-pipelines must be labelled
# azure.workload.identity/use=true and annotated with this client_id, and the
# Azure AD application must carry a federated credential trusting the cluster's
# OIDC issuer for subject:
#   system:serviceaccount:tekton-pipelines:facets-workflows-sa
provider "facets" {
  azure = {
    subscription_id       = var.subscription_id
    tenant_id             = var.tenant_id
    client_id             = var.client_id
    use_workload_identity = true
  }
}

resource "facets_tekton_action_azure" "stop_database" {
  name        = "stop-database"
  description = "Stops the PostgreSQL Flexible Server to save compute cost"

  facets_resource_name = var.instance_name

  facets_environment = {
    unique_name = var.environment_unique_name
  }

  facets_resource = {
    kind = "postgres"
  }

  steps = [{
    name  = "stop-flexible-server"
    image = "mcr.microsoft.com/azure-cli:2.89.1"

    resources = {
      requests = { cpu = "100m", memory = "256Mi" }
      limits   = { cpu = "500m", memory = "512Mi" }
    }

    env = [
      {
        name  = "RESOURCE_GROUP"
        value = var.resource_group_name
      },
      {
        name  = "SERVER_NAME"
        value = var.server_name
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

output "task_name" {
  value = facets_tekton_action_azure.stop_database.task_name
}
