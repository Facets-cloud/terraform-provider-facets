# Terraform Provider for Facets

This Terraform provider implements resources for managing Facets infrastructure, specifically Tekton Tasks and StepActions for Kubernetes-based workflows.

## Features

- **In-cluster Kubernetes authentication priority**: Automatically uses service account tokens when running in a Kubernetes cluster
- **Tekton Task management**: Create and manage Tekton Tasks with custom steps and parameters
- **No submodule dependency bloat**: Direct resource implementation avoids dependency issues from nested submodules

## Authentication Priority

The provider uses the following priority order for Kubernetes authentication:

1. In-cluster config (service account token) - **takes precedence**
2. KUBECONFIG environment variable
3. ~/.kube/config file

This ensures that when running inside a Kubernetes cluster, the provider automatically uses the mounted service account token.

## Resources

### `facets_tekton_action_kubernetes`

Creates a Tekton Task and StepAction for Kubernetes-based workflows.

#### Schema

- `name` (String, Required): Display name of the Tekton Task
- `description` (String, Optional): Description of the Tekton Task
- `instance_name` (String, Required): Resource instance name
- `environment` (Dynamic, Required): Environment object (any type)
- `instance` (Dynamic, Required): Instance object (any type)
- `namespace` (String, Optional): Kubernetes namespace for Tekton resources (default: "tekton-pipelines")
- `steps` (List of Objects, Required): List of steps for the Tekton Task
  - `name` (String, Required): Step name
  - `image` (String, Required): Container image for the step
  - `script` (String, Required): Script to execute in the step
  - `resources` (Dynamic, Required): Resource requests and limits (any type)
  - `env` (List of Objects, Optional): Environment variables for the step
    - `name` (String, Required): Environment variable name
    - `value` (String, Required): Environment variable value
- `params` (Dynamic, Optional): List of params for the Tekton Task (any type)

#### Computed Attributes

- `id` (String): Resource identifier
- `task_name` (String): Generated Tekton Task name
- `step_action_name` (String): Generated StepAction name

#### Example Usage

```hcl
terraform {
  required_providers {
    facets = {
      source = "facets-cloud/facets"
    }
  }
}

provider "facets" {}

resource "facets_tekton_action_kubernetes" "rollout_restart" {
  name          = "rollout-restart"
  description   = "Rollout restart deployments in Kubernetes"
  instance_name = "my-service"

  environment = {
    unique_name = "production"
  }

  instance = {
    kind = "service"
  }

  steps = [
    {
      name  = "restart-deployments"
      image = "bitnami/kubectl:latest"
      resources = {}
      env = [
        {
          name  = "RESOURCE_TYPE"
          value = "deployment"
        },
        {
          name  = "RESOURCE_NAME"
          value = "my-app"
        },
        {
          name  = "NAMESPACE"
          value = "default"
        }
      ]
      script = <<-EOT
        #!/bin/bash
        set -e
        echo "Starting Kubernetes deployment rollout restart workflow..."

        LABEL_SELECTOR="resourceType=$RESOURCE_TYPE,resourceName=$RESOURCE_NAME"
        DEPLOYMENTS=$(kubectl get deployments -n $NAMESPACE -l "$LABEL_SELECTOR" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')

        if [ -z "$DEPLOYMENTS" ]; then
          echo "No deployments found"
          exit 0
        fi

        while IFS= read -r name; do
          if [ -n "$name" ]; then
            kubectl rollout restart deployment "$name" -n "$NAMESPACE"
            kubectl rollout status deployment "$name" -n "$NAMESPACE" --timeout=300s
          fi
        done <<< "$DEPLOYMENTS"
      EOT
    }
  ]

  params = [
    {
      name = "CUSTOM_PARAM"
      type = "string"
      description = "A custom parameter"
      default = "default-value"
    }
  ]
}

output "task_name" {
  value = facets_tekton_action_kubernetes.rollout_restart.task_name
}
```

## Installation

See [INSTALL.md](INSTALL.md) for detailed installation instructions.

**Quick Install from GitHub Releases:**

```bash
# Download from releases page
wget https://github.com/facets-cloud/terraform-provider-facets/releases/download/v0.1.0/terraform-provider-facets_0.1.0_linux_amd64.zip

# Extract and install
unzip terraform-provider-facets_0.1.0_linux_amd64.zip
mkdir -p ~/.terraform.d/plugins/github.com/facets-cloud/facets/0.1.0/linux_amd64
mv terraform-provider-facets_v0.1.0 ~/.terraform.d/plugins/github.com/facets-cloud/facets/0.1.0/linux_amd64/terraform-provider-facets
chmod +x ~/.terraform.d/plugins/github.com/facets-cloud/facets/0.1.0/linux_amd64/terraform-provider-facets
```

Then use in your Terraform:
```hcl
terraform {
  required_providers {
    facets = {
      source  = "github.com/facets-cloud/facets"
      version = "~> 0.1.0"
    }
  }
}
```

## Building from Source

```bash
make install
```

Or manually:

```bash
go build -o terraform-provider-facets
```

## Development

The provider is built using the Terraform Plugin Framework and follows these key principles:

1. **Direct resources over submodules**: To avoid dependency bloat, all resources are implemented directly rather than using nested submodules
2. **In-cluster authentication priority**: Service account tokens are automatically used when available
3. **Type flexibility**: Uses dynamic types for fields that accept any structure (environment, instance, params, resources)

## Project Structure

```
.
├── internal/
│   ├── k8s/
│   │   └── client.go          # Kubernetes client with auth priority
│   └── provider/
│       ├── provider.go         # Provider implementation
│       └── resource_tekton_action_kubernetes.go  # Tekton resource
├── main.go                     # Provider entry point
├── go.mod                      # Go module dependencies
└── README.md                   # This file
```

## References

This provider replaces the Terraform module at:
`github.com/Facets-cloud/facets-utility-modules//actions/kubernetes`

By implementing the functionality as a native Terraform provider resource instead of a module with submodules, we avoid the dependency bloat issue described in the [Facets IAC wiki](https://github.com/Facets-cloud/facets-iac/wiki/Terraform-Submodule-Dependency-Bloat).
