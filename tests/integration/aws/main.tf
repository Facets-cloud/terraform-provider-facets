terraform {
  required_providers {
    facets = {
      source  = "Facets-cloud/facets"
      version = "99.0.0-local"
    }
  }
}

# AWS provider configuration is REQUIRED for facets_tekton_action_aws
variable "aws_region" {
  description = "AWS region for provider configuration"
  type        = string
}

variable "aws_access_key" {
  description = "AWS access key for provider configuration"
  type        = string
  sensitive   = true
}

variable "aws_secret_key" {
  description = "AWS secret key for provider configuration"
  type        = string
  sensitive   = true
}

provider "facets" {
  aws = {
    region     = var.aws_region
    access_key = var.aws_access_key
    secret_key = var.aws_secret_key
  }
}

# Changes: name, description, added parameter, env var values changed, new env var
resource "facets_tekton_action_aws" "integration_test" {
  # Required attributes (UPDATED)
  name                 = "integration-test-action"
  description          = "Integration test for AWS action - EC2 restart (UPDATES)"
  facets_resource_name = "integration-test-app"

  # Facets environment object
  facets_environment = {
    unique_name = "integration-test-env"
  }

  # Facets resource object (all fields)
  facets_resource = {
    kind    = "application"
    flavor  = "aws"
    version = "1.0.0"
    spec = {
      tier = "backend"
      team = "platform"
    }
  }

  # Optional namespace
  namespace = "tekton-pipelines"

  params = [
    {
      name = "INSTANCE_ID"
      type = "string"
    }
  ]

  # Multiple steps demonstrating AWS CLI usage
  steps = [
    {
      name  = "restart-instance"
      image = "amazon/aws-cli:latest"

      resources = {
        requests = {
          cpu    = "200m"
          memory = "256Mi"
        }
      }

      env = [
        {
          name  = "ACTION"
          value = "restart"
        },
        {
          name  = "AWS_REGION"
          value = var.aws_region
        }
      ]

      script = <<-EOT
        #!/bin/bash
        set -e

        echo "=== Restarting EC2 Instance (UPDATED) ==="
        INSTANCE_ID="$(params.INSTANCE_ID)"
        echo "Instance ID: $INSTANCE_ID"
        echo "Action: $ACTION"

        # In a real scenario, this would restart the instance
        # For integration tests, we just validate the command would work
        echo "Simulating restart command for testing..."
        echo "Command: aws ec2 reboot-instances --instance-ids $INSTANCE_ID --region $AWS_REGION"

        aws ec2 stop-instances \
          --instance-ids "$INSTANCE_ID" \
          --region "$AWS_REGION"

        echo "Stop command executed"

        stop_elapsed=0
        while true; do
          STATE=$(aws ec2 describe-instances \
            --instance-ids "$INSTANCE_ID" \
            --query 'Reservations[0].Instances[0].State.Name' \
            --output text)

          echo "Current state: $STATE"

          if [ "$STATE" == "stopped" ]; then
            echo "Instance is stopped"
            break
          fi

          if [ "$stop_elapsed" -ge 120 ]; then
            echo "Timeout waiting for instance to be stopped"
            break
          fi

          echo "Waiting for instance to be stopped..."
          sleep 10
        done

        aws ec2 start-instances \
          --instance-ids "$INSTANCE_ID" \
          --region "$AWS_REGION"

        echo "=== Verifying Instance Status ==="
        echo "Instance ID: $INSTANCE_ID"

        elapsed=0
        while true; do
          STATE=$(aws ec2 describe-instances \
            --instance-ids "$INSTANCE_ID" \
            --query 'Reservations[0].Instances[0].State.Name' \
            --output text)

          echo "Current state: $STATE"

          if [ "$STATE" == "running" ]; then
            echo "Instance is running"
            break
          fi

          if [ "$elapsed" -ge 120 ]; then
            echo "Timeout waiting for instance to be running"
            break
          fi

          echo "Waiting for instance to be running..."
          sleep 10
        done
      EOT
    }
  ]
}

# Test all outputs
output "test_id" {
  description = "Resource ID"
  value       = facets_tekton_action_aws.integration_test.id
}

output "test_task_name" {
  description = "Generated Task name"
  value       = facets_tekton_action_aws.integration_test.task_name
}

output "test_step_action_name" {
  description = "Generated StepAction name"
  value       = facets_tekton_action_aws.integration_test.step_action_name
}

output "test_display_name" {
  description = "Display name"
  value       = facets_tekton_action_aws.integration_test.name
}

output "test_namespace" {
  description = "Namespace"
  value       = facets_tekton_action_aws.integration_test.namespace
}
