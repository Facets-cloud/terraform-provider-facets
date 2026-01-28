#!/bin/bash
set -e

# Helper script to set up AWS credentials and run integration tests
# This script prompts for AWS credentials and runs the test

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}AWS Integration Test Setup${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""

# Check if credentials are already set
if [ -n "$TF_VAR_aws_region" ] && [ -n "$TF_VAR_aws_access_key" ] && [ -n "$TF_VAR_aws_secret_key" ]; then
    echo -e "${GREEN}✓${NC} AWS credentials already set via environment variables"
    echo "  Region: $TF_VAR_aws_region"
    echo ""
    read -p "Use existing credentials? (y/n) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        echo -e "${BLUE}Running tests with existing credentials...${NC}"
        echo ""
        $PWD/tests/integration/aws/test.sh
        exit 0
    fi
fi

# Prompt for AWS credentials
echo "Please provide AWS credentials for testing:"
echo ""

# AWS Region
read -p "AWS Region (default: us-east-1): " aws_region
aws_region=${aws_region:-us-east-1}

# AWS Access Key
read -p "AWS Access Key ID: " aws_access_key
if [ -z "$aws_access_key" ]; then
    echo -e "${RED}✗${NC} AWS Access Key is required"
    exit 1
fi

# AWS Secret Key (hidden input)
read -s -p "AWS Secret Access Key: " aws_secret_key
echo
if [ -z "$aws_secret_key" ]; then
    echo -e "${RED}✗${NC} AWS Secret Key is required"
    exit 1
fi

echo ""
echo -e "${GREEN}✓${NC} Credentials provided"
echo ""

# Validate credentials (optional - test with AWS CLI if available)
if command -v aws &> /dev/null; then
    echo -e "${BLUE}Validating credentials...${NC}"
    export AWS_ACCESS_KEY_ID="$aws_access_key"
    export AWS_SECRET_ACCESS_KEY="$aws_secret_key"
    export AWS_DEFAULT_REGION="$aws_region"

    if aws sts get-caller-identity &> /dev/null; then
        echo -e "${GREEN}✓${NC} AWS credentials are valid"
        IDENTITY=$(aws sts get-caller-identity --query 'Arn' --output text)
        echo "  Authenticated as: $IDENTITY"
    else
        echo -e "${YELLOW}!${NC} Warning: Could not validate credentials (may still work)"
        read -p "Continue anyway? (y/n) " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            echo "Aborted"
            exit 1
        fi
    fi
    echo ""
fi

# Export credentials for Terraform
export TF_VAR_aws_region="$aws_region"
export TF_VAR_aws_access_key="$aws_access_key"
export TF_VAR_aws_secret_key="$aws_secret_key"

echo -e "${BLUE}Running integration tests...${NC}"
echo ""

# Run the test script
$PWD/tests/integration/aws/test.sh

# Clean up exported variables (optional)
unset TF_VAR_aws_region
unset TF_VAR_aws_access_key
unset TF_VAR_aws_secret_key
unset AWS_ACCESS_KEY_ID
unset AWS_SECRET_ACCESS_KEY
unset AWS_DEFAULT_REGION

echo ""
echo -e "${GREEN}Done!${NC}"
