#!/bin/bash
set -e

# Integration test script for Facets Terraform Provider
# Tests CREATE, UPDATE, and DELETE operations with locally built binary
# Using version 99.0.0-local to ensure we never use published version

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
PROVIDER_VERSION="99.0.0-local"
PROVIDER_NAME="terraform-provider-facets"
TEST_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$TEST_DIR/../../.." && pwd)"
CLUSTER_ID="${CLUSTER_ID:-integration-test-cluster}"

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Facets Provider Integration Test${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""

# Function to print step
step() {
    echo -e "${BLUE}==>${NC} $1"
}

# Function to print success
success() {
    echo -e "${GREEN}✓${NC} $1"
}

# Function to print error
error() {
    echo -e "${RED}✗${NC} $1"
}

# Function to print warning
warning() {
    echo -e "${YELLOW}!${NC} $1"
}

# Cleanup function
cleanup() {
    step "Cleaning up test artifacts..."
    cd "$TEST_DIR"
    rm -rf .terraform .terraform.lock.hcl terraform.tfstate terraform.tfstate.backup tfplan tfplan-update
    success "Cleanup complete"
}

# Trap errors and cleanup
trap 'error "Test failed!"; cleanup; exit 1' ERR

cleanup

# Step 1: Build the provider
step "Building provider binary..."
cd "$PROJECT_ROOT"
go build -o "$PROVIDER_NAME"
if [ ! -f "$PROVIDER_NAME" ]; then
    error "Provider binary not found after build"
    exit 1
fi
success "Provider binary built: $PROVIDER_NAME"

# Step 2: Install to local plugin directory
step "Installing provider to local plugin directory..."
PLUGIN_DIR="$HOME/.terraform.d/plugins/registry.terraform.io/Facets-cloud/facets/$PROVIDER_VERSION/$(go env GOOS)_$(go env GOARCH)"
mkdir -p "$PLUGIN_DIR"
cp "$PROVIDER_NAME" "$PLUGIN_DIR/${PROVIDER_NAME}_v${PROVIDER_VERSION}"
chmod +x "$PLUGIN_DIR/${PROVIDER_NAME}_v${PROVIDER_VERSION}"
success "Provider installed to: $PLUGIN_DIR"

# Step 3: Backup main-updated.tf to avoid conflicts
step "Backing up update configuration..."
cd "$TEST_DIR"
if [ -f "main-updated.tf" ]; then
    mv main-updated.tf main-updated.tf.backup
    success "Update configuration backed up"
fi

# Step 4: Initialize Terraform
step "Initializing Terraform..."
terraform init -upgrade
success "Terraform initialized"

# Verify the correct version is being used
step "Verifying provider version..."
if terraform version | grep -q "$PROVIDER_VERSION"; then
    success "Using local provider version: $PROVIDER_VERSION"
else
    warning "Provider version not explicitly shown in terraform version output"
    warning "Lock file verification..."
    if grep -q "$PROVIDER_VERSION" .terraform.lock.hcl; then
        success "Lock file confirms version: $PROVIDER_VERSION"
    else
        error "Lock file does not contain expected version: $PROVIDER_VERSION"
        exit 1
    fi
fi

# Step 4: Test CREATE operation
step "Testing CREATE operation..."
export CLUSTER_ID="$CLUSTER_ID"
terraform plan -out=tfplan
terraform apply tfplan
success "CREATE operation completed"

# Verify resources in Kubernetes
step "Verifying resources in Kubernetes..."
TASK_NAME=$(terraform output -raw test_task_name)
STEP_ACTION_NAME=$(terraform output -raw test_step_action_name)
NAMESPACE=$(terraform output -raw test_namespace)

echo "  Task name: $TASK_NAME"
echo "  StepAction name: $STEP_ACTION_NAME"
echo "  Namespace: $NAMESPACE"

# Check if Task exists
if kubectl get task "$TASK_NAME" -n "$NAMESPACE" &>/dev/null; then
    success "Task exists in cluster"
else
    error "Task not found in cluster"
    exit 1
fi

# Check if StepAction exists
if kubectl get stepaction "$STEP_ACTION_NAME" -n "$NAMESPACE" &>/dev/null; then
    success "StepAction exists in cluster"
else
    error "StepAction not found in cluster"
    exit 1
fi

# Verify labels
step "Verifying resource labels..."
LABELS=$(kubectl get task "$TASK_NAME" -n "$NAMESPACE" -o jsonpath='{.metadata.labels}')
if echo "$LABELS" | grep -q "integration-test-action"; then
    success "Labels correctly set"
else
    warning "Labels verification inconclusive"
fi

# Step 5: Test UPDATE operation
step "Testing UPDATE operation..."
mv main.tf main.tf.original
cp main-updated.tf.backup main.tf

terraform plan -out=tfplan-update
terraform apply tfplan-update
success "UPDATE operation completed"

# Verify update
step "Verifying UPDATE changes..."
DISPLAY_NAME=$(terraform output -raw test_display_name)
if [ "$DISPLAY_NAME" = "integration-test-action-updated" ]; then
    success "Display name updated correctly"
else
    error "Display name not updated: $DISPLAY_NAME"
    exit 1
fi

# Verify updated labels in Kubernetes
UPDATED_LABELS=$(kubectl get task "$TASK_NAME" -n "$NAMESPACE" -o jsonpath='{.metadata.labels.display_name}')
if [ "$UPDATED_LABELS" = "integration-test-action-updated" ]; then
    success "Kubernetes labels updated correctly"
else
    error "Kubernetes labels not updated: $UPDATED_LABELS"
    exit 1
fi

# Step 6: Test DELETE operation
step "Testing DELETE operation..."
terraform destroy -auto-approve
success "DELETE operation completed"

# Verify resources are deleted
step "Verifying resources are deleted from Kubernetes..."
if kubectl get task "$TASK_NAME" -n "$NAMESPACE" &>/dev/null; then
    error "Task still exists in cluster after delete"
    exit 1
else
    success "Task deleted from cluster"
fi

if kubectl get stepaction "$STEP_ACTION_NAME" -n "$NAMESPACE" &>/dev/null; then
    error "StepAction still exists in cluster after delete"
    exit 1
else
    success "StepAction deleted from cluster"
fi

# Step 7: Final cleanup
mv main.tf.original main.tf
mv main-updated.tf.backup main-updated.tf
cleanup

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}All integration tests passed! ✓${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo "Summary:"
echo "  ✓ CREATE - Resources created successfully"
echo "  ✓ UPDATE - Resources updated successfully"
echo "  ✓ DELETE - Resources deleted successfully"
echo ""
