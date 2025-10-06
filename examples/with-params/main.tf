terraform {
  required_providers {
    facets = {
      source  = "Facets-cloud/facets"
      version = "~> 0.1"
    }
  }
}

provider "facets" {}

# Example showing how to define custom parameters
# Parameters can be passed when triggering the action via Facets UI
resource "facets_tekton_action_kubernetes" "scale" {
  name                 = "scale-deployment"
  description          = "Scale a Kubernetes deployment to specified replicas"
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

  # Define custom parameters that users will provide
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
      name = "NAMESPACE"
      type = "string"
    }
  ]

  steps = [
    {
      name  = "scale"
      image = "bitnami/kubectl:latest"

      # Parameters are available as environment variables
      script = <<-EOT
        #!/bin/bash
        set -e

        echo "Scaling deployment $DEPLOYMENT_NAME to $REPLICAS replicas..."

        kubectl scale deployment/$DEPLOYMENT_NAME \
          --replicas=$REPLICAS \
          -n $NAMESPACE

        echo "Waiting for rollout to complete..."
        kubectl rollout status deployment/$DEPLOYMENT_NAME \
          -n $NAMESPACE \
          --timeout=5m

        echo "Successfully scaled to $REPLICAS replicas"
      EOT
    }
  ]
}

output "task_name" {
  value = facets_tekton_action_kubernetes.scale.task_name
}
