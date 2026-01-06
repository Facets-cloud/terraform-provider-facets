# Local Testing Guide

This guide explains how to test the Terraform Provider for Facets locally during development.

## Prerequisites

- Go 1.21 or later
- Terraform CLI v1.0 or later
- (For integration tests) Kubernetes cluster with Tekton installed
- (For integration tests) `kubectl` configured with cluster access

## Quick Start

```bash
# Clone the repository
git clone https://github.com/Facets-cloud/terraform-provider-facets.git
cd terraform-provider-facets

# Run unit tests (no Kubernetes required)
make test

# Build the provider
go build -o terraform-provider-facets
```

## Testing Methods

### 1. Unit Tests (Recommended First Step)

Unit tests validate the provider logic without requiring a Kubernetes cluster.

```bash
# Run all unit tests
make test

# Run with verbose output
go test -v ./internal/provider/

# Run specific test
go test -v ./internal/provider/ -run TestBuildLabels
```

**What's tested:**
- Resource name generation
- Label building and merging
- Metadata extraction
- Input validation (namespace format, env var names)

### 2. Dev Overrides (Fastest for Development)

Dev overrides let Terraform use your locally built binary without running `terraform init`.

**Step 1: Create or update `~/.terraformrc`**

```hcl
provider_installation {
  dev_overrides {
    "Facets-cloud/facets" = "/path/to/terraform-provider-facets"
  }
  direct {}
}
```

Replace `/path/to/terraform-provider-facets` with your actual project path, e.g.:
```hcl
"Facets-cloud/facets" = "/Users/vishnukv/facets/codebases/terraform-provider-facets"
```

**Step 2: Build the provider**

```bash
go build -o terraform-provider-facets
```

**Step 3: Test with an example**

```bash
cd examples/multiple-steps
terraform plan
```

> **Note:** With dev overrides, you'll see a warning: "Provider development overrides are in effect". This is expected.

**Step 4: Apply (requires Kubernetes + Tekton)**

```bash
terraform apply
```

### 3. Local Plugin Installation

Install the provider to Terraform's plugin directory for a more production-like test.

**Option A: Using Makefile**

```bash
make install
```

This installs to `~/.terraform.d/plugins/github.com/facets-cloud/facets/0.1.0/<OS_ARCH>/`

**Option B: Manual installation**

```bash
# Build
go build -o terraform-provider-facets

# Create plugin directory (adjust OS/arch as needed)
mkdir -p ~/.terraform.d/plugins/localhost/facets-cloud/facets/1.0.0/darwin_arm64

# Copy binary
cp terraform-provider-facets ~/.terraform.d/plugins/localhost/facets-cloud/facets/1.0.0/darwin_arm64/
```

**Using locally installed provider:**

```hcl
terraform {
  required_providers {
    facets = {
      source  = "localhost/facets-cloud/facets"
      version = "1.0.0"
    }
  }
}
```

Then run:
```bash
terraform init
terraform plan
terraform apply
```

### 4. Integration Tests (Full Lifecycle)

Integration tests validate the complete CREATE → UPDATE → DELETE lifecycle against a real Kubernetes cluster.

**Prerequisites:**
- Kubernetes cluster with Tekton CRDs installed
- `kubectl` configured and working

**Run integration tests:**

```bash
# From project root
./tests/integration/test.sh

# With custom cluster ID
CLUSTER_ID="my-test-cluster" ./tests/integration/test.sh
```

**What the integration test does:**
1. Builds the provider binary
2. Installs to local plugin directory (version `99.0.0-local`)
3. Runs `terraform apply` to CREATE resources
4. Verifies resources exist with `kubectl`
5. Modifies config and runs `terraform apply` for UPDATE
6. Verifies updates applied correctly
7. Runs `terraform destroy` to DELETE resources
8. Verifies resources are removed

## Testing Specific Features

### Testing Custom Labels

```hcl
resource "facets_tekton_action_kubernetes" "test" {
  name                 = "test-labels"
  facets_resource_name = "my-app"

  facets_environment = {
    unique_name = "dev"
  }

  facets_resource = {
    kind = "application"
  }

  # Custom labels to test
  labels = {
    "team"        = "platform"
    "cost-center" = "engineering"
  }

  steps = [
    {
      name   = "test"
      image  = "busybox:latest"
      script = "echo 'Hello World'"
    }
  ]
}
```

After applying, verify labels:
```bash
kubectl get tasks -n tekton-pipelines -o jsonpath='{.items[*].metadata.labels}' | jq
```

### Testing Resource Limits

```hcl
steps = [
  {
    name  = "limited-step"
    image = "busybox:latest"

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

    script = "echo 'Testing resources'"
  }
]
```

### Testing Custom Parameters

```hcl
params = [
  {
    name = "DEPLOYMENT_NAME"
    type = "string"
  },
  {
    name = "REPLICAS"
    type = "string"
  }
]
```

## Debugging

### Enable Terraform Logging

```bash
export TF_LOG=DEBUG
terraform plan
```

### Check Created Kubernetes Resources

```bash
# List all Tekton Tasks
kubectl get tasks -n tekton-pipelines

# Describe a specific task
kubectl describe task <task-name> -n tekton-pipelines

# List StepActions
kubectl get stepactions -n tekton-pipelines

# View task YAML
kubectl get task <task-name> -n tekton-pipelines -o yaml
```

### Common Issues

**Issue: "Provider not found"**
- Ensure binary is built: `go build -o terraform-provider-facets`
- Check `~/.terraformrc` path is correct
- Verify binary exists at the specified path

**Issue: "Kubernetes connection refused"**
- Verify `kubectl` works: `kubectl get nodes`
- Check KUBECONFIG is set or `~/.kube/config` exists

**Issue: "Tekton CRDs not found"**
- Install Tekton: `kubectl apply -f https://storage.googleapis.com/tekton-releases/pipeline/latest/release.yaml`
- Verify CRDs exist: `kubectl get crd tasks.tekton.dev`

## Cleaning Up

```bash
# Remove test resources
terraform destroy

# Clean Terraform state files
make clean

# Remove dev overrides (edit ~/.terraformrc)
```

## Directory Structure

```
terraform-provider-facets/
├── examples/                    # Example configurations
│   ├── basic/                   # Simple hello-world
│   ├── multiple-steps/          # Multi-step pipeline with labels
│   ├── with-params/             # Custom parameters
│   └── with-resources/          # Resource limits
├── tests/
│   └── integration/
│       └── kubernetes/          # Integration test suite
├── internal/
│   └── provider/                # Provider implementation
├── docs/
│   ├── resources/               # Resource documentation
│   └── LOCAL_TESTING.md         # This file
├── Makefile                     # Build commands
└── README.md                    # Main documentation
```

## Makefile Commands

| Command | Description |
|---------|-------------|
| `make build` | Build the provider binary |
| `make install` | Build and install to plugin directory |
| `make test` | Run unit tests |
| `make clean` | Remove Terraform state files |

## Further Reading

- [Resource Documentation](resources/tekton_action_kubernetes.md)
- [Installation Guide](../INSTALL.md)
- [Integration Test README](../tests/integration/README.md)
- [Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework)
