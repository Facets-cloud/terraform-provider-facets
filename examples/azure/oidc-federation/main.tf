terraform {
  required_providers {
    facets = {
      source = "Facets-cloud/facets"
    }
  }
}

# Preferred: OIDC federation. Microsoft Entra ID exchanges the pod's projected
# service-account token for an Azure token, so no client secret exists anywhere.
# This is the Azure analogue of the AWS variant's IRSA flow, and it works
# cross-cloud -- a pod in an EKS cluster can authenticate to Azure.
#
# Prerequisites:
#   1. A federated identity credential on the app registration, trusting the
#      cluster's OIDC issuer with subject
#      system:serviceaccount:<namespace>:<serviceaccount> and audience
#      api://AzureADTokenExchange.
#   2. The TaskRun pod must mount a projected service-account token with that
#      same audience (reusing the default token fails with AADSTS700212).
provider "facets" {
  azure = {
    subscription_id     = var.subscription_id
    tenant_id           = var.tenant_id
    client_id           = var.client_id
    use_oidc_federation = true
  }
}

# Note: no `params` block. The user clicks the action in the Facets UI and never
# supplies credentials -- the setup-credentials step authenticates the Azure CLI
# for every step that follows.
resource "facets_tekton_action_azure" "stop_database" {
  name                 = "stop-database"
  description          = "Stops the Azure MySQL Flexible Server (Azure auto-restarts after 7 days)"
  facets_resource_name = "main-db"

  facets_environment = {
    unique_name = "production"
  }

  facets_resource = {
    kind = "mysql"
  }

  steps = [
    {
      name  = "stop"
      image = "mcr.microsoft.com/azure-cli:2.61.0"

      resources = {
        requests = { cpu = "100m", memory = "256Mi" }
        limits   = { cpu = "500m", memory = "512Mi" }
      }

      script = <<-EOT
        #!/bin/bash
        set -euo pipefail

        SERVER_NAME="main-db-mysql-production"
        RESOURCE_GROUP="my-resource-group"

        STATE=$(az mysql flexible-server show \
          --name "$SERVER_NAME" --resource-group "$RESOURCE_GROUP" \
          --query state --output tsv)

        if [ "$STATE" = "Stopped" ]; then
          echo "Already stopped; nothing to do."
          exit 0
        fi

        az mysql flexible-server stop \
          --name "$SERVER_NAME" --resource-group "$RESOURCE_GROUP" --output none
        echo "Stop issued."
      EOT
    }
  ]
}

output "task_name" {
  value = facets_tekton_action_azure.stop_database.task_name
}
