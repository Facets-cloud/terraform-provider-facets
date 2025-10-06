# Terraform Provider for Facets

The Facets Terraform Provider allows you to manage Tekton-based Kubernetes workflows integrated with the Facets platform.

## Features

- 🚀 **Automated Credential Management** - Kubernetes credentials are automatically injected based on user RBAC
- 🔧 **Simple Configuration** - Define workflows using familiar Terraform syntax
- 🎯 **Tekton Integration** - Creates Tekton Tasks and StepActions automatically
- 📊 **Blueprint Mapping** - Seamlessly maps to Facets blueprint resources
- 🔐 **RBAC-Scoped** - User permissions enforced automatically
- **In-cluster Kubernetes authentication priority**: Automatically uses service account tokens when running in a Kubernetes cluster
- **No submodule dependency bloat**: Direct resource implementation avoids dependency issues from nested submodules

## Authentication Priority

The provider uses the following priority order for Kubernetes authentication:

1. In-cluster config (service account token) - **takes precedence**
2. KUBECONFIG environment variable
3. ~/.kube/config file

This ensures that when running inside a Kubernetes cluster, the provider automatically uses the mounted service account token.

## How It Works

When a user triggers an action via the Facets UI:

1. **Facets UI** provides user's kubeconfig (base64-encoded) as `FACETS_USER_KUBECONFIG` parameter
2. **Credential Setup** step automatically decodes and configures kubectl access
3. **Your Steps** execute with kubectl configured according to user's RBAC permissions
4. **Labels** track the action back to Facets blueprint resources

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `CLUSTER_ID` | No | `"na"` | Cluster identifier for resource labeling |

## Resources

### `facets_tekton_action_kubernetes`

Creates a Tekton Task and StepAction for Kubernetes-based workflows.

#### Schema

- `name` (String, Required): Display name of the Tekton Task
- `description` (String, Optional): Description of the Tekton Task
- `facets_resource_name` (String, Required): Resource name as defined in the Facets blueprint
- `facets_environment` (Object, Required): Facets-managed environment configuration
  - `unique_name` (String, Required): Unique name of the environment
- `facets_resource` (Object, Required): Resource definition as specified in the Facets blueprint
  - `kind` (String, Required): Resource kind
  - `flavor` (String, Required): Resource flavor
  - `version` (String, Required): Resource version
  - `spec` (Dynamic, Required): Additional resource specifications
- `namespace` (String, Optional): Kubernetes namespace for Tekton resources (default: "tekton-pipelines")
- `steps` (List of Objects, Required): List of steps for the Tekton Task
  - `name` (String, Required): Step name
  - `image` (String, Required): Container image for the step
  - `script` (String, Required): Script to execute in the step
  - `resources` (Object, Optional): Compute resources for the step
    - `requests` (Map of Strings, Optional): Minimum compute resources (e.g., cpu, memory)
    - `limits` (Map of Strings, Optional): Maximum compute resources
  - `env` (List of Objects, Optional): Environment variables for the step
    - `name` (String, Required): Environment variable name
    - `value` (String, Required): Environment variable value
- `params` (List of Objects, Optional): List of custom parameters for the Tekton Task
  - `name` (String, Required): Parameter name
  - `type` (String, Required): Parameter type (e.g., "string", "array")

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
  name                 = "rollout-restart"
  description          = "Rollout restart deployments in Kubernetes"
  facets_resource_name = "my-service"

  facets_environment = {
    unique_name = "production"
  }

  facets_resource = {
    kind    = "service"
    flavor  = "k8s"
    version = "1.0"
    spec    = {}
  }

  steps = [
    {
      name  = "restart-deployments"
      image = "bitnami/kubectl:latest"

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

}
```

For more detailed documentation and additional examples, see:
- [Resource Documentation](docs/resources/tekton_action_kubernetes.md)
- [Examples Directory](examples/)

## Auto-Injected Parameters

Every Tekton Task automatically includes these parameters (no need to define them):

- `FACETS_USER_EMAIL` - Email of the user triggering the action
- `FACETS_USER_KUBECONFIG` - Base64-encoded kubeconfig with RBAC permissions

These parameters are populated by the Facets UI when the action is triggered.

## Auto-Generated Steps

A `setup-credentials` step is automatically prepended to your workflow that:
- Decodes the base64-encoded kubeconfig
- Places it at `/workspace/.kube/config`
- Sets `KUBECONFIG` environment variable for all steps

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

### Running Tests

**Unit Tests:**
```bash
go test -v ./internal/provider/
```

**Integration Tests:**
```bash
./tests/integration/test.sh
```

See [tests/integration/README.md](tests/integration/README.md) for detailed integration test documentation.

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
