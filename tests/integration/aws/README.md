# AWS Action Integration Tests

This directory contains integration tests for the `facets_tekton_action_aws` resource. These tests verify the full CREATE → UPDATE → DELETE lifecycle using a locally built provider binary with AWS credentials.

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

5. **AWS Credentials**: Valid AWS credentials for testing
   - Access Key ID
   - Secret Access Key
   - Region (e.g., us-east-1)

## Running the Tests

### Quick Start

From this directory:
```bash
# Set AWS credentials
export TF_VAR_aws_region="us-east-1"
export TF_VAR_aws_access_key="your-access-key-here"
export TF_VAR_aws_secret_key="your-secret-key-here"

# Run tests
./test.sh
```

### What the Test Does

The integration test performs the following operations:

1. **Build** - Builds the provider binary from source
2. **Install** - Installs to local Terraform plugin directory with version `99.0.0-local`
3. **Create** - Creates Tekton Task and StepAction resources with AWS credentials
4. **Verify Create** - Confirms resources exist in Kubernetes with correct attributes
5. **Verify AWS Credentials** - Confirms StepAction has hardcoded AWS credentials
6. **Update** - Updates the resources (changes description, adds param, modifies env vars)
7. **Verify Update** - Confirms updates are applied correctly
8. **Delete** - Destroys all resources
9. **Verify Delete** - Confirms resources are removed from Kubernetes
10. **Cleanup** - Removes test artifacts

### Using a Custom Cluster ID

```bash
CLUSTER_ID="my-test-cluster" ./test.sh
```

### Test Configuration

The test uses two Terraform configurations:

- **`main.tf`** - Initial configuration for CREATE (EC2 restart example)
- **`main-updated.tf`** - Modified configuration for UPDATE (adds TIMEOUT parameter)

Both configurations use **all available provider attributes**:
- Required fields: name, description, facets_resource_name
- Facets objects: facets_environment, facets_resource
- Optional: namespace, params
- Steps with: resources (requests/limits), env vars, AWS CLI scripts
- **Provider AWS block**: region, access_key, secret_key

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
  StepAction name: setup-aws-credentials-abc123...
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

## Key Differences from Kubernetes Tests

### AWS-Specific Features Tested

1. **Provider AWS Configuration**
   - AWS region, access_key, secret_key must be configured in provider block
   - Credentials passed via Terraform variables (not hardcoded)

2. **StepAction with Hardcoded Credentials**
   - StepAction script contains embedded AWS credentials
   - No parameters needed (unlike Kubernetes action)
   - Credentials visible in `kubectl get stepaction -o yaml`

3. **AWS Environment Variables**
   - `AWS_CONFIG_FILE=/workspace/.aws/config`
   - `AWS_SHARED_CREDENTIALS_FILE=/workspace/.aws/credentials`
   - NOT `KUBECONFIG` (that's for Kubernetes action)

4. **File-Based Authentication**
   - StepAction creates `~/.aws/credentials` and `~/.aws/config`
   - AWS CLI/SDK reads from these files automatically

5. **EC2 Use Case**
   - Test demonstrates EC2 instance restart workflow
   - Uses `amazon/aws-cli` image
   - Validates AWS CLI access to EC2 API

## Troubleshooting

### Test Fails: AWS Configuration Required

**Issue**: Error "AWS configuration is required for facets_tekton_action_aws resource"

**Solution**: Ensure AWS credentials are exported:
```bash
export TF_VAR_aws_region="us-east-1"
export TF_VAR_aws_access_key="your-access-key"
export TF_VAR_aws_secret_key="your-secret-key"
```

### Test Fails: AWS Credentials Invalid

**Issue**: AWS CLI commands fail in StepAction

**Check**:
```bash
# Verify credentials work locally
aws sts get-caller-identity --region us-east-1

# Check StepAction script
kubectl get stepaction <name> -n tekton-pipelines -o yaml
# Look for credentials in script (they should be present)
```

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

### Credentials Exposed in Kubernetes

**Expected Behavior**: AWS credentials ARE visible in the StepAction resource in Kubernetes:
```bash
kubectl get stepaction <name> -n tekton-pipelines -o yaml
# You will see credentials in the script section
```

This is **intentional and acceptable** because:
- Credentials NOT stored in Terraform state
- Access controlled by Kubernetes RBAC
- Same security model as Kubernetes Secrets

### Provider Binary Not Found

**Issue**: Build step fails

**Solution**:
```bash
# Build manually from project root
cd ../../..
go build -o terraform-provider-facets
```

## Manual Testing

If you want to run the test manually step-by-step:

```bash
# 1. Build provider
cd ../../..
go build -o terraform-provider-facets

# 2. Install to plugin directory
VERSION="99.0.0-local"
PLUGIN_DIR="$HOME/.terraform.d/plugins/registry.terraform.io/Facets-cloud/facets/$VERSION/$(go env GOOS)_$(go env GOARCH)"
mkdir -p "$PLUGIN_DIR"
cp terraform-provider-facets "$PLUGIN_DIR/terraform-provider-facets_v$VERSION"

# 3. Set AWS credentials
export TF_VAR_aws_region="us-east-1"
export TF_VAR_aws_access_key="your-access-key"
export TF_VAR_aws_secret_key="your-secret-key"

# 4. Run Terraform commands
cd tests/integration/aws
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
cd tests/integration/aws

# Clean Terraform artifacts
rm -rf .terraform .terraform.lock.hcl terraform.tfstate*

# Restore original config
mv main.tf.original main.tf 2>/dev/null || true

# Clean Kubernetes resources (if test failed mid-way)
kubectl delete tasks -l cluster_id=integration-test-cluster -n tekton-pipelines
kubectl delete stepactions -l cluster_id=integration-test-cluster -n tekton-pipelines
```

## What Gets Tested

### CREATE Operation
- ✅ Provider binary builds successfully
- ✅ Provider installs to correct plugin directory
- ✅ Terraform initializes with local provider
- ✅ AWS configuration validated from provider block
- ✅ Resources created in Kubernetes (Task + StepAction)
- ✅ StepAction contains hardcoded AWS credentials in script
- ✅ Task has no AWS parameters (credentials hardcoded, not passed)
- ✅ All attributes applied correctly
- ✅ Labels set correctly
- ✅ Computed outputs (task_name, step_action_name, id)

### UPDATE Operation
- ✅ Resources update without recreation
- ✅ Changed attributes propagate to Kubernetes
- ✅ Labels update correctly
- ✅ New parameters added successfully
- ✅ Environment variables update correctly
- ✅ Outputs reflect updated values

### DELETE Operation
- ✅ Both Task and StepAction deleted
- ✅ Resources removed from Kubernetes
- ✅ No orphaned resources

## Security Notes

### Credentials in Terraform State
- ❌ AWS credentials are **NOT** stored in Terraform state
- ✅ Only Task/StepAction names are stored
- ✅ Provider marks credentials as `Sensitive: true`

### Credentials in Kubernetes
- ✅ AWS credentials **ARE** embedded in StepAction script
- ✅ Visible via `kubectl get stepaction -o yaml`
- ✅ Access controlled by Kubernetes RBAC
- ✅ Same security model as Kubernetes Secrets

### Credentials in Test Output
- ✅ Terraform marks variables as sensitive (not printed)
- ✅ Test script doesn't echo credentials

## Test Scenarios

### Scenario 1: EC2 Instance Restart
The default test configuration demonstrates restarting an EC2 instance:
1. Validates instance exists via AWS CLI
2. Prepares restart command (not executed in test)
3. Verifies instance state

### Scenario 2: Custom Parameters
The UPDATE test adds a TIMEOUT parameter:
1. Shows how to add new parameters dynamically
2. Demonstrates parameter usage in scripts

### Scenario 3: Environment Variables
Both configs test environment variable injection:
1. User-defined env vars (LOG_LEVEL, RETRY_COUNT)
2. Auto-injected AWS env vars (AWS_CONFIG_FILE, AWS_SHARED_CREDENTIALS_FILE)

## Notes

- Tests use namespace `tekton-pipelines` by default
- Cluster ID defaults to `integration-test-cluster`
- Test resources are prefixed with `integration-test-`
- Provider version `99.0.0-local` ensures isolation from published versions
- Script exits on first error (set -e)
- EC2 restart commands are **simulated** (not actually executed)
- Real credentials required but instance ID can be fake for testing

## CI/CD Integration

To run in CI/CD pipelines:

```yaml
# GitHub Actions example
- name: Run AWS Integration Tests
  run: |
    # Ensure kubectl is configured
    kubectl cluster-info

    # Set AWS credentials from secrets
    export TF_VAR_aws_region="${{ secrets.AWS_REGION }}"
    export TF_VAR_aws_access_key="${{ secrets.AWS_ACCESS_KEY }}"
    export TF_VAR_aws_secret_key="${{ secrets.AWS_SECRET_KEY }}"

    # Run tests
    cd tests/integration/aws
    ./test.sh
  env:
    CLUSTER_ID: ci-test-cluster-${{ github.run_id }}
```
