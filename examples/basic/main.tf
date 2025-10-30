terraform {
  required_providers {
    facets = {
      source  = "Facets-cloud/facets"
      version = "~> 0.1"
    }
  }
}

provider "facets" {}

# Basic example of a Tekton action
# This creates a simple "hello world" workflow
resource "facets_tekton_action_kubernetes" "hello" {
  name                 = "hello-world"
  description          = "Simple hello world action"
  facets_resource_name = "my-app"

  facets_environment = {
    unique_name = "dev"
  }

  facets_resource = {
    kind = "application"
  }

  steps = [
    {
      name  = "greet"
      image = "busybox:latest"

      script = <<-EOT
        #!/bin/sh
        echo "Hello from Facets!"
        echo "Current time: $(date)"
        echo "Pod: $(hostname)"
      EOT
    }
  ]
}

output "task_name" {
  value = facets_tekton_action_kubernetes.hello.task_name
}
