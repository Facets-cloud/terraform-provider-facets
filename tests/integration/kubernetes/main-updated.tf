terraform {
  required_providers {
    facets = {
      source  = "Facets-cloud/facets"
      version = "99.0.0-local"
    }
  }
}

provider "facets" {}

# UPDATED version - tests UPDATE operation
# Changes: description, name, added param, env var values changed, new env var
resource "facets_tekton_action_kubernetes" "integration_test" {
  # Required attributes (UPDATED)
  name                 = "integration-test-action-updated"
  description          = "Integration test for all provider attributes - UPDATED VERSION"
  facets_resource_name = "integration-test-app"

  # Facets environment object
  facets_environment = {
    unique_name = "integration-test-env"
  }

  # Facets resource object (all fields)
  facets_resource = {
    kind    = "application"
    flavor  = "kubernetes"
    version = "1.0.0"
    spec = {
      tier = "backend"
      team = "platform"
    }
  }

  # Optional namespace
  namespace = "tekton-pipelines"

  # Custom parameters (UPDATED - added one more)
  params = [
    {
      name = "DEPLOYMENT_NAME"
      type = "string"
    },
    {
      name = "REPLICAS"
      type = "string"
    },
    {
      name = "ENVIRONMENT"
      type = "string"
    },
    {
      name = "VERSION"
      type = "string"
    }
  ]

  # Multiple steps with all step attributes
  steps = [
    # Step 1: With resources and env vars (UPDATED env values)
    {
      name  = "validate"
      image = "bitnami/kubectl:latest"

      resources = {
        requests = {
          cpu    = "100m"
          memory = "128Mi"
        }
        limits = {
          cpu    = "500m"
          memory = "512Mi"
        }
      }

      env = [
        {
          name  = "LOG_LEVEL"
          value = "debug"
        },
        {
          name  = "NAMESPACE"
          value = "production"
        },
        {
          name  = "UPDATED"
          value = "true"
        }
      ]

      script = <<-EOT
        #!/bin/bash
        set -e
        echo "Validating deployment..."
        echo "Log level: $LOG_LEVEL"
        echo "Namespace: $NAMESPACE"
        kubectl version --client
        echo "Validation complete"
      EOT
    },

    # Step 2: With only requests (no limits)
    {
      name  = "deploy"
      image = "bitnami/kubectl:latest"

      resources = {
        requests = {
          cpu    = "200m"
          memory = "256Mi"
        }
      }

      env = [
        {
          name  = "DEPLOYMENT"
          value = "test-deployment"
        }
      ]

      script = <<-EOT
        #!/bin/bash
        set -e
        echo "Deploying application..."
        echo "Deployment: $DEPLOYMENT"
        echo "Using parameters:"
        echo "  DEPLOYMENT_NAME: $DEPLOYMENT_NAME"
        echo "  REPLICAS: $REPLICAS"
        echo "  ENVIRONMENT: $ENVIRONMENT"
        kubectl get nodes
        echo "Deployment simulated"
      EOT
    },

    # Step 3: Minimal (no resources, no env)
    {
      name  = "verify"
      image = "busybox:latest"

      script = <<-EOT
        #!/bin/sh
        echo "Verification step"
        echo "KUBECONFIG is set to: $KUBECONFIG"
        echo "All steps completed successfully"
      EOT
    }
  ]
}

# Test all outputs
output "test_id" {
  description = "Resource ID"
  value       = facets_tekton_action_kubernetes.integration_test.id
}

output "test_task_name" {
  description = "Generated Task name"
  value       = facets_tekton_action_kubernetes.integration_test.task_name
}

output "test_step_action_name" {
  description = "Generated StepAction name"
  value       = facets_tekton_action_kubernetes.integration_test.step_action_name
}

output "test_display_name" {
  description = "Display name"
  value       = facets_tekton_action_kubernetes.integration_test.name
}

output "test_namespace" {
  description = "Namespace"
  value       = facets_tekton_action_kubernetes.integration_test.namespace
}
