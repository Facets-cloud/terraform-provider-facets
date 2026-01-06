# facets_tekton_action_kubernetes

Manages a Tekton Task and StepAction for Kubernetes-based workflows in Facets.

This resource automatically creates:
- A Tekton Task with your specified workflow steps
- A StepAction that sets up Kubernetes credentials automatically

## How It Works

When a user triggers this action via the Facets UI:

1. **Automatic Credential Injection**: The Facets UI automatically populates the `FACETS_USER_KUBECONFIG` parameter with the user's kubeconfig (base64 encoded)
2. **RBAC-Scoped Access**: The kubeconfig is scoped to the user's Role-Based Access Control (RBAC) permissions
3. **Credential Setup**: A `setup-credentials` step is automatically prepended to your workflow that:
   - Decodes the base64-encoded kubeconfig
   - Places it at `/workspace/.kube/config`
   - Sets the `KUBECONFIG` environment variable for all subsequent steps
4. **Your Steps Run**: Your defined workflow steps execute with kubectl access configured

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `CLUSTER_ID` | No | `"na"` | Cluster identifier added to resource labels for tracking |

## Example Usage

### Basic Example

```hcl
resource "facets_tekton_action_kubernetes" "hello_world" {
  name                 = "hello-world"
  description          = "Simple hello world workflow"
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
      name   = "greet"
      image  = "busybox:latest"
      script = <<-EOT
        #!/bin/sh
        echo "Hello from Facets!"
      EOT
    }
  ]
}
```

### With Resource Limits

```hcl
resource "facets_tekton_action_kubernetes" "deploy" {
  name                 = "deploy-application"
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
        kubectl apply -f deployment.yaml
        kubectl rollout status deployment/my-app
      EOT
    }
  ]
}
```

### With Environment Variables

```hcl
resource "facets_tekton_action_kubernetes" "restart" {
  name                 = "restart-deployment"
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
      name  = "restart"
      image = "bitnami/kubectl:latest"

      env = [
        {
          name  = "DEPLOYMENT_NAME"
          value = "my-app"
        },
        {
          name  = "NAMESPACE"
          value = "default"
        }
      ]

      script = <<-EOT
        #!/bin/bash
        kubectl rollout restart deployment/$DEPLOYMENT_NAME -n $NAMESPACE
        kubectl rollout status deployment/$DEPLOYMENT_NAME -n $NAMESPACE
      EOT
    }
  ]
}
```

### With Custom Parameters

```hcl
resource "facets_tekton_action_kubernetes" "scale" {
  name                 = "scale-deployment"
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

  params = [
    {
      name = "REPLICAS"
      type = "string"
    },
    {
      name = "DEPLOYMENT_NAME"
      type = "string"
    }
  ]

  steps = [
    {
      name  = "scale"
      image = "bitnami/kubectl:latest"

      script = <<-EOT
        #!/bin/bash
        kubectl scale deployment/$DEPLOYMENT_NAME --replicas=$REPLICAS
        kubectl rollout status deployment/$DEPLOYMENT_NAME
      EOT
    }
  ]
}
```

### With Custom Labels

```hcl
resource "facets_tekton_action_kubernetes" "deploy_with_labels" {
  name                 = "deploy-application"
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

  # Custom labels for tracking and organization
  labels = {
    "team"        = "platform"
    "cost-center" = "engineering"
    "owner"       = "devops"
    "tier"        = "critical"
  }

  steps = [
    {
      name  = "deploy"
      image = "bitnami/kubectl:latest"

      script = <<-EOT
        #!/bin/bash
        kubectl apply -f deployment.yaml
        kubectl rollout status deployment/my-app
      EOT
    }
  ]
}
```

### Multiple Steps

```hcl
resource "facets_tekton_action_kubernetes" "ci_pipeline" {
  name                 = "ci-pipeline"
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

  steps = [
    {
      name  = "build"
      image = "docker:latest"
      script = <<-EOT
        #!/bin/sh
        echo "Building application..."
        docker build -t my-app:latest .
      EOT
    },
    {
      name  = "test"
      image = "my-app:latest"
      script = <<-EOT
        #!/bin/sh
        echo "Running tests..."
        npm test
      EOT
    },
    {
      name  = "deploy"
      image = "bitnami/kubectl:latest"
      script = <<-EOT
        #!/bin/bash
        echo "Deploying to Kubernetes..."
        kubectl apply -f k8s/
        kubectl rollout status deployment/my-app
      EOT
    }
  ]
}
```

## Argument Reference

### Required Arguments

* `name` - (String) Display name of the Tekton Task. This is a human-readable identifier.
* `facets_resource_name` - (String) Resource name as defined in the Facets blueprint. Used to map the Tekton task back to the blueprint resource in Facets.
* `facets_environment` - (Object) Facets-managed environment configuration. Required fields:
  * `unique_name` - (String) Unique name of the Facets-managed environment
* `facets_resource` - (Object) Resource definition as specified in the Facets blueprint. Required fields:
  * `kind` - (String) Resource kind
  * `flavor` - (String) Resource flavor
  * `version` - (String) Resource version
  * `spec` - (Dynamic) Additional resource specifications (can be empty object)
* `steps` - (List of Objects) List of workflow steps to execute. Each step requires:
  * `name` - (String) Step name
  * `image` - (String) Container image for the step
  * `script` - (String) Script to execute in the step

### Optional Arguments

* `description` - (String) Description of the Tekton Task
* `namespace` - (String) Kubernetes namespace for Tekton resources. Defaults to `"tekton-pipelines"`
* `labels` - (Map of Strings) Custom labels to apply to the Tekton Task and StepAction resources. These labels are merged with auto-generated labels (`display_name`, `resource_name`, `resource_kind`, `environment_unique_name`, `cluster_id`). Auto-generated labels take precedence and cannot be overwritten.
* `params` - (List of Objects) List of custom parameters for the Tekton Task. Each parameter has:
  * `name` - (String) Parameter name
  * `type` - (String) Parameter type (e.g., "string", "array")

### Optional Step Arguments

* `resources` - (Object) Compute resources for the step:
  * `requests` - (Map of Strings) Minimum compute resources required (e.g., `{cpu = "100m", memory = "128Mi"}`)
  * `limits` - (Map of Strings) Maximum compute resources allowed (e.g., `{cpu = "500m", memory = "512Mi"}`)
* `env` - (List of Objects) Environment variables for the step:
  * `name` - (String) Environment variable name
  * `value` - (String) Environment variable value

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - Resource identifier in format `namespace/task_name`
* `task_name` - Generated Tekton Task name (computed from hash of resource_name, environment, and name). This is the actual Kubernetes resource name and may be truncated to 63 characters.
* `step_action_name` - Generated StepAction name for credential setup (computed from hash). This StepAction automatically configures Kubernetes access for the workflow steps.

## Auto-Injected Parameters

The following parameters are automatically added to every Tekton Task and do not need to be specified:

* `FACETS_USER_EMAIL` - (String) Email of the user triggering the action
* `FACETS_USER_KUBECONFIG` - (String) Base64-encoded kubeconfig with user's RBAC permissions

These parameters are populated by the Facets UI when the action is triggered.

## Import

Tekton actions can be imported using the format `namespace/task_name`:

```shell
terraform import facets_tekton_action_kubernetes.example tekton-pipelines/2f5a8b9c1d3e4f6a7b8c9d0e1f2a3b4c
```

Note: The task name is a hash-based identifier. You can find it by running:

```shell
kubectl get tasks -n tekton-pipelines
```
