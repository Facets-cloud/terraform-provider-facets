# Integration Tests

This directory contains integration tests for the Facets Terraform Provider. These tests verify the full CREATE → UPDATE → DELETE lifecycle using a locally built provider binary.

## Prerequisites

1. **Kubernetes Cluster**: Access to a Kubernetes cluster with Tekton installed
   ```bash
   kubectl version
   kubectl get ns tekton-pipelines  # Should exist
   ```

2. **Terraform**: Terraform CLI installed (v1.0+)
   ```bash
   terraform version
   ```

3. **Go**: Go toolchain (for building the provider)
   ```bash
   go version
   ```

4. **kubectl**: Configured to access your Kubernetes cluster
   ```bash
   kubectl cluster-info
   ```

## Running the Tests

### Quick Start

From the project root:
```bash
./tests/integration/test.sh
```

### What the Test Does

The integration test performs the following operations:

1. **Build** - Builds the provider binary from source
2. **Install** - Installs to local Terraform plugin directory with version `99.0.0-local`
3. **Create** - Creates Tekton Task and StepAction resources
4. **Verify Create** - Confirms resources exist in Kubernetes with correct attributes
5. **Update** - Updates the resources (changes description, adds param, modifies env vars)
6. **Verify Update** - Confirms updates are applied correctly
7. **Delete** - Destroys all resources
8. **Verify Delete** - Confirms resources are removed from Kubernetes
9. **Cleanup** - Removes test artifacts

### Using a Custom Cluster ID

```bash
CLUSTER_ID="my-test-cluster" ./tests/integration/test.sh
```

### Test Configuration

The test uses two Terraform configurations:

- **`main.tf`** - Initial configuration for CREATE
- **`main-updated.tf`** - Modified configuration for UPDATE

Both configurations use **all available provider attributes**:
- Required fields: name, description, facets_resource_name
- Facets objects: facets_environment, facets_resource
- Optional: namespace, params
- Steps with: resources (requests/limits), env vars, scripts

### Provider Version

The test uses a special version **`99.0.0-local`** to ensure:
- Never conflicts with published versions
- Always uses locally built binary
- Easy to identify in logs/lock files

## Test Output

Successful test output:
```
========================================
Facets Provider Integration Test
========================================

==> Building provider binary...
✓ Provider binary built: terraform-provider-facets
==> Installing provider to local plugin directory...
✓ Provider installed to: ~/.terraform.d/plugins/...
==> Initializing Terraform...
✓ Terraform initialized
==> Testing CREATE operation...
✓ CREATE operation completed
==> Verifying resources in Kubernetes...
  Task name: abc123...
  StepAction name: setup-credentials-abc123...
  Namespace: tekton-pipelines
✓ Task exists in cluster
✓ StepAction exists in cluster
==> Testing UPDATE operation...
✓ UPDATE operation completed
==> Verifying UPDATE changes...
✓ Display name updated correctly
✓ Kubernetes labels updated correctly
==> Testing DELETE operation...
✓ DELETE operation completed
==> Verifying resources are deleted from Kubernetes...
✓ Task deleted from cluster
✓ StepAction deleted from cluster

========================================
All integration tests passed! ✓
========================================
```

## Troubleshooting

### Test Fails at CREATE

**Issue**: Resources not created in Kubernetes

**Check**:
```bash
# Verify kubectl access
kubectl get nodes

# Check Tekton is installed
kubectl get ns tekton-pipelines

# Check RBAC permissions
kubectl auth can-i create tasks -n tekton-pipelines
kubectl auth can-i create stepactions -n tekton-pipelines
```

### Test Fails at UPDATE

**Issue**: Resources not updating

**Check**:
```bash
# Manually verify resource exists
kubectl get task -n tekton-pipelines

# Check for resource version conflicts
kubectl describe task <task-name> -n tekton-pipelines
```

### Provider Binary Not Found

**Issue**: Build step fails

**Solution**:
```bash
# Build manually from project root
cd ../..
go build -o terraform-provider-facets
```

### Wrong Provider Version Used

**Issue**: Test uses published version instead of local

**Check**:
```bash
# Check lock file
cat tests/integration/.terraform.lock.hcl | grep version

# Should show: version = "99.0.0-local"
```

**Fix**:
```bash
# Clean and re-run
cd tests/integration
rm -rf .terraform .terraform.lock.hcl
./test.sh
```

## Manual Testing

If you want to run the test manually step-by-step:

```bash
# 1. Build provider
go build -o terraform-provider-facets

# 2. Install to plugin directory
VERSION="99.0.0-local"
PLUGIN_DIR="$HOME/.terraform.d/plugins/registry.terraform.io/Facets-cloud/facets/$VERSION/$(go env GOOS)_$(go env GOARCH)"
mkdir -p "$PLUGIN_DIR"
cp terraform-provider-facets "$PLUGIN_DIR/terraform-provider-facets_v$VERSION"

# 3. Run Terraform commands
cd tests/integration
export CLUSTER_ID="test-cluster"
terraform init -upgrade
terraform apply          # CREATE
cp main-updated.tf main.tf
terraform apply          # UPDATE
terraform destroy        # DELETE
```

## Cleaning Up

The test script automatically cleans up after itself, but if needed:

```bash
cd tests/integration

# Clean Terraform artifacts
rm -rf .terraform .terraform.lock.hcl terraform.tfstate*

# Restore original config
mv main.tf.original main.tf 2>/dev/null || true

# Clean Kubernetes resources (if test failed mid-way)
kubectl delete tasks -l cluster_id=integration-test-cluster -n tekton-pipelines
kubectl delete stepactions -l cluster_id=integration-test-cluster -n tekton-pipelines
```

## CI/CD Integration

To run in CI/CD pipelines:

```yaml
# GitHub Actions example
- name: Run Integration Tests
  run: |
    # Ensure kubectl is configured
    kubectl cluster-info

    # Run tests
    ./tests/integration/test.sh
  env:
    CLUSTER_ID: ci-test-cluster-${{ github.run_id }}
```

## What Gets Tested

### CREATE Operation
- ✅ Provider binary builds successfully
- ✅ Provider installs to correct plugin directory
- ✅ Terraform initializes with local provider
- ✅ Resources created in Kubernetes (Task + StepAction)
- ✅ All attributes applied correctly
- ✅ Labels set correctly
- ✅ Computed outputs (task_name, step_action_name, id)

### UPDATE Operation
- ✅ Resources update without recreation
- ✅ Changed attributes propagate to Kubernetes
- ✅ Labels update correctly
- ✅ ResourceVersion handling (optimistic locking)
- ✅ Outputs reflect updated values

### DELETE Operation
- ✅ Both Task and StepAction deleted
- ✅ Resources removed from Kubernetes
- ✅ No orphaned resources

## Notes

- Tests use namespace `tekton-pipelines` by default
- Cluster ID defaults to `integration-test-cluster`
- Test resources are prefixed with `integration-test-`
- Provider version `99.0.0-local` ensures isolation from published versions
- Script exits on first error (set -e)
