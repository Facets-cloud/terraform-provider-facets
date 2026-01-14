terraform {
  required_providers {
    facets = {
      source  = "Facets-cloud/facets"
      version = "~> 0.1"
    }
  }
}

provider "facets" {}

# Example showing a multi-step CI/CD pipeline with custom labels
resource "facets_tekton_action_kubernetes" "ci_pipeline" {
  name                 = "ci-pipeline"
  description          = "Multi-step CI/CD pipeline"
  facets_resource_name = "my-app"

  facets_environment = {
    unique_name = "staging"
  }

  facets_resource = {
    kind    = "application"
    flavor  = "k8s"
    version = "1.0"
    spec    = {}
  }

  # Custom labels for organization and tracking
  # These merge with auto-generated labels (display_name, resource_name, etc.)
  labels = {
    "team"        = "platform"
    "cost-center" = "engineering"
    "tier"        = "non-production"
  }

  # Multiple steps execute in sequence
  steps = [
    {
      name  = "validate"
      image = "bitnami/kubectl:latest"

      script = <<-EOT
        #!/bin/bash
        set -e
        echo "Validating Kubernetes manifests..."
        kubectl apply --dry-run=client -f /workspace/k8s/
        echo "✓ Manifests are valid"
      EOT
    },
    {
      name  = "deploy"
      image = "bitnami/kubectl:latest"

      resources = {
        requests = {
          cpu    = "100m"
          memory = "128Mi"
        }
      }

      script = <<-EOT
        #!/bin/bash
        set -e
        echo "Applying Kubernetes manifests..."
        kubectl apply -f /workspace/k8s/
        echo "✓ Resources applied"
      EOT
    },
    {
      name  = "verify"
      image = "bitnami/kubectl:latest"

      script = <<-EOT
        #!/bin/bash
        set -e
        echo "Waiting for deployment to be ready..."
        kubectl rollout status deployment/my-app --timeout=5m

        echo "Verifying pods are running..."
        kubectl get pods -l app=my-app

        echo "✓ Deployment successful"
      EOT
    },
    {
      name  = "smoke-test"
      image = "curlimages/curl:latest"

      env = [
        {
          name  = "SERVICE_URL"
          value = "http://my-app-service"
        }
      ]

      script = <<-EOT
        #!/bin/sh
        set -e
        echo "Running smoke tests..."

        # Test health endpoint
        curl -f "$SERVICE_URL/health" || exit 1
        echo "✓ Health check passed"

        # Test API endpoint
        curl -f "$SERVICE_URL/api/version" || exit 1
        echo "✓ API check passed"

        echo "✓ All smoke tests passed"
      EOT
    }
  ]
}

output "task_name" {
  value = facets_tekton_action_kubernetes.ci_pipeline.task_name
}

output "display_name" {
  value = facets_tekton_action_kubernetes.ci_pipeline.name
}
