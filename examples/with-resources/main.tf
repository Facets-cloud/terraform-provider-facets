terraform {
  required_providers {
    facets = {
      source  = "Facets-cloud/facets"
      version = "~> 0.1"
    }
  }
}

provider "facets" {}

# Example showing how to specify CPU and memory resource limits
resource "facets_tekton_action_kubernetes" "deploy" {
  name                 = "deploy-with-resources"
  description          = "Deployment action with resource constraints"
  facets_resource_name = "my-app"

  facets_environment = {
    unique_name = "production"
  }

  facets_resource = {
    kind    = "application"
    flavor  = "k8s"
    version = "1.0"
    spec    = {}
  }

  steps = [
    {
      name  = "deploy"
      image = "bitnami/kubectl:latest"

      # Specify resource requests and limits
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

      script = <<-EOT
        #!/bin/bash
        set -e

        echo "Deploying application..."
        kubectl apply -f /workspace/manifests/

        echo "Waiting for rollout..."
        kubectl rollout status deployment/my-app --timeout=5m

        echo "Deployment complete!"
      EOT
    }
  ]
}

output "task_name" {
  value = facets_tekton_action_kubernetes.deploy.task_name
}
