# An action that returns a result. The module declares the action and its
# result schema in facets.yaml; `action_key` must match that declaration.
#
# With `result = true` every step receives FACETS_RESULT_FILE. Write exactly one
# JSON object to it -- matching the result schema, under roughly 3.5 KB -- and
# Tekton captures it as the `facets_result` Task result for Facets to validate.

terraform {
  required_providers {
    facets = {
      source  = "Facets-cloud/facets"
      version = "~> 1.3"
    }
  }
}

provider "facets" {}

variable "environment_unique_name" {
  type        = string
  description = "Unique name of the Facets environment"
}

variable "instance_name" {
  type        = string
  description = "Blueprint resource name the actions attach to"
}

variable "db_instance_id" {
  type        = string
  description = "RDS instance identifier"
}

variable "namespace" {
  type        = string
  description = "Namespace of the deployment to inspect"
}

variable "deployment_name" {
  type        = string
  description = "Deployment to inspect"
}

# Cloud-agnostic resource: reports an RDS instance's status.
resource "facets_actions" "db_status" {
  name        = "Database Status"
  description = "Reports the RDS instance status"
  action_key  = "db_status"
  result      = true

  facets_resource_name = var.instance_name
  facets_environment   = { unique_name = var.environment_unique_name }
  facets_resource      = { kind = "postgres" }

  steps = [{
    name  = "status"
    image = "amazon/aws-cli:2.31.9"
    env   = { DB_INSTANCE_ID = var.db_instance_id }

    script = <<-EOT
      #!/bin/bash
      set -euo pipefail
      STATE=$(aws rds describe-db-instances \
        --db-instance-identifier "$DB_INSTANCE_ID" \
        --query 'DBInstances[0].DBInstanceStatus' --output text)
      jq -n --arg id "$DB_INSTANCE_ID" --arg state "$STATE" \
        '{instance_id: $id, status: $state}' > "$FACETS_RESULT_FILE"
    EOT
  }]
}

# Kubernetes resource: reports a deployment's rollout state. The generated
# setup-credentials step is a StepAction ref and does not get FACETS_RESULT_FILE;
# every step declared here does.
resource "facets_tekton_action_kubernetes" "rollout_status" {
  name       = "rollout-status"
  action_key = "rollout_status"
  result     = true

  facets_resource_name = var.instance_name
  facets_environment   = { unique_name = var.environment_unique_name }
  facets_resource      = { kind = "service" }

  steps = [{
    name  = "status"
    image = "bitnami/kubectl:latest"
    env = [
      { name = "NAMESPACE", value = var.namespace },
      { name = "DEPLOYMENT", value = var.deployment_name },
    ]

    script = <<-EOT
      #!/bin/bash
      set -euo pipefail
      READY=$(kubectl get deployment "$DEPLOYMENT" -n "$NAMESPACE" -o jsonpath='{.status.readyReplicas}')
      WANTED=$(kubectl get deployment "$DEPLOYMENT" -n "$NAMESPACE" -o jsonpath='{.spec.replicas}')
      jq -n --argjson ready "$${READY:-0}" --argjson wanted "$WANTED" \
        '{ready_replicas: $ready, desired_replicas: $wanted}' > "$FACETS_RESULT_FILE"
    EOT
  }]
}
