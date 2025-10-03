terraform {
  required_providers {
    facets = {
      source = "localhost/facets-cloud/facets"
    }
  }
}

provider "facets" {}

resource "facets_tekton_action_kubernetes" "hello_world" {
  name          = "hello-world"
  description   = "Simple hello world workflow"
  instance_name = "hello-world-app"

  environment = {
    unique_name = "dev"
  }

  instance = {
    kind = "workflow"
  }

  namespace = "tekton-pipelines"

  steps = [
    {
      name      = "print-hello"
      image     = "busybox:latest"
      resources = {}
      env       = []
      script    = <<-EOT
        #!/bin/sh
        echo "Hello, World!"
        echo "This is a Facets Tekton action running in Kubernetes"
        echo "Current time: $(date)"
        echo "Hostname: $(hostname)"
      EOT
    }
  ]

  params = []
}

output "task_name" {
  description = "The generated Tekton Task name"
  value       = facets_tekton_action_kubernetes.hello_world.task_name
}

output "step_action_name" {
  description = "The generated StepAction name"
  value       = facets_tekton_action_kubernetes.hello_world.step_action_name
}

output "task_id" {
  description = "The resource ID (namespace/task_name)"
  value       = facets_tekton_action_kubernetes.hello_world.id
}
