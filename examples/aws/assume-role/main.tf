terraform {
  required_providers {
    facets = {
      source  = "Facets-cloud/facets"
      version = "99.0.0-local"
    }
  }
}

# Configure the Facets provider with AWS assume_role
# This example demonstrates using IAM role assumption with ambient/pod credentials
# The pod must have IAM permissions (via IRSA, instance profile, etc.) to assume the role
provider "facets" {
  aws = {
    region = var.aws_region

    # Assume an IAM role to get temporary credentials
    # The pod's ambient credentials (IRSA, instance profile, etc.) will be used
    # to assume this role at Task runtime
    assume_role = {
      role_arn     = var.role_arn
      session_name = var.session_name
      external_id  = var.external_id  # Optional: required if role trust policy specifies it
      duration     = 3600             # Optional: 1 hour (default), max 12 hours
    }
  }
}
resource "facets_tekton_action_aws" "integration_test" {
  name                 = "integration-test-action"
  description          = "Integration test for AWS action - EC2 restart"
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
        aws sts get-caller-identity

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

# Output the generated resource names
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
