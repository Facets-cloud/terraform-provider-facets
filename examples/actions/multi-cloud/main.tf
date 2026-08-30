# One resource type, three clouds. The provider is given no cloud configuration
# at all -- credentials reach the pod from the runner's environment, via a Secret
# the provider maintains, so nothing sensitive passes through Terraform.
#
#   export FACETS_ACTION_CRED_AWS_ACCESS_KEY_ID=...
#   export FACETS_ACTION_CRED_AZURE_CLIENT_SECRET=...
#   export FACETS_ACTION_CRED_GOOGLE_APPLICATION_CREDENTIALS=/creds.json

terraform {
  required_providers {
    facets = {
      source  = "Facets-cloud/facets"
      version = "~> 1.3"
    }
  }
}

provider "facets" {}

locals {
  environment = { unique_name = var.environment_unique_name }
}

resource "facets_actions" "stop_rds" {
  name         = "Stop Database"
  description  = "Stops the RDS instance outside business hours"
  cloud_action = true

  facets_resource_name = var.instance_name
  facets_environment   = local.environment
  facets_resource      = { kind = "postgres" }

  steps = [{
    name  = "stop"
    image = "amazon/aws-cli:2.31.9"
    env   = { DB_INSTANCE_ID = var.db_instance_id }

    # AWS_* arrive from the credentials Secret; the CLI picks them up itself.
    script = <<-EOT
      #!/bin/bash
      set -e
      STATE=$(aws rds describe-db-instances \
        --db-instance-identifier "$DB_INSTANCE_ID" \
        --query 'DBInstances[0].DBInstanceStatus' --output text)
      [ "$STATE" = "stopped" ] && { echo "Already stopped"; exit 0; }
      aws rds stop-db-instance --db-instance-identifier "$DB_INSTANCE_ID"
    EOT
  }]
}

resource "facets_actions" "stop_flexible_server" {
  name         = "Stop Database"
  description  = "Stops the PostgreSQL Flexible Server outside business hours"
  cloud_action = true

  facets_resource_name = var.instance_name
  facets_environment   = local.environment
  facets_resource      = { kind = "postgres" }

  steps = [{
    name  = "stop"
    image = "mcr.microsoft.com/azure-cli:2.89.1"
    env = {
      RESOURCE_GROUP = var.resource_group_name
      SERVER_NAME    = var.server_name
    }

    script = <<-EOT
      #!/bin/bash
      set -e
      az login --service-principal \
        --username "$AZURE_CLIENT_ID" \
        --password "$AZURE_CLIENT_SECRET" \
        --tenant   "$AZURE_TENANT_ID" \
        --output none
      az postgres flexible-server stop \
        --resource-group "$RESOURCE_GROUP" --name "$SERVER_NAME"
    EOT
  }]
}

# No cloud credentials needed: the pod's own service account is enough, so the
# provider attaches no Secret and cloud_action stays false.
resource "facets_actions" "restart_deployment" {
  name        = "Restart Service"
  description = "Rolling restart of the deployment"

  facets_resource_name = var.instance_name
  facets_environment   = local.environment
  facets_resource      = { kind = "service" }

  steps = [{
    name  = "restart"
    image = "bitnamilegacy/kubectl:1.33.4"
    env = {
      NAMESPACE  = var.namespace
      DEPLOYMENT = var.deployment_name
    }
    script = <<-EOT
      #!/bin/bash
      set -e
      kubectl rollout restart deployment "$DEPLOYMENT" -n "$NAMESPACE"
      kubectl rollout status  deployment "$DEPLOYMENT" -n "$NAMESPACE" --timeout=300s
    EOT
  }]
}
