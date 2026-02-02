# AWS Assume Role Example

This example demonstrates how to use the `facets_tekton_action_aws` resource with IAM role assumption for temporary credentials.

## Overview

When using `assume_role`, the provider:
1. Uses the pod's ambient credentials (IRSA, instance profile, environment variables) to authenticate to AWS
2. Calls AWS STS AssumeRole at Task runtime to obtain temporary credentials
3. Uses temporary credentials for all AWS operations in the workflow

This is useful for:
- **Cross-account access**: Assume a role in a different AWS account
- **Enhanced security**: Use temporary credentials that expire automatically
- **Least privilege**: Assume roles with specific, limited permissions
- **Auditing**: Track role assumptions in CloudTrail
- **IRSA support**: Leverage IAM Roles for Service Accounts in EKS

## Prerequisites

### 1. AWS IAM Role Setup

Create an IAM role with the appropriate trust policy:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "AWS": "arn:aws:iam::YOUR-ACCOUNT-ID:user/your-user"
      },
      "Action": "sts:AssumeRole",
      "Condition": {
        "StringEquals": {
          "sts:ExternalId": "my-external-id"
        }
      }
    }
  ]
}
```

### 2. Pod IAM Permissions

The pod executing the Tekton Task must have IAM permissions to assume the role.
This can be achieved through:

#### Option A: IRSA (IAM Roles for Service Accounts) - RECOMMENDED for EKS
```bash
# Associate IAM role with Kubernetes service account
eksctl create iamserviceaccount \
  --name tekton-bot \
  --namespace tekton-pipelines \
  --cluster my-cluster \
  --attach-policy-arn arn:aws:iam::aws:policy/custom-assume-role-policy \
  --approve
```

#### Option B: EC2 Instance Profile
Attach an instance profile to your EC2 nodes with `sts:AssumeRole` permission:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "sts:AssumeRole",
      "Resource": "arn:aws:iam::123456789012:role/my-cross-account-role"
    }
  ]
}
```

#### Option C: Environment Variables (for testing)
Set AWS credentials in the pod's environment:
```bash
AWS_ACCESS_KEY_ID=AKIA...
AWS_SECRET_ACCESS_KEY=...
```

### 3. StepAction Image Requirements

The StepAction image must have:
- **AWS CLI v2** installed
- **jq** installed (for JSON parsing)

The default `facetscloud/actions-base-image:v1.0.0` should include these tools.

## Usage

### 1. Copy the example configuration

```bash
cp terraform.tfvars.example terraform.tfvars
```

### 2. Edit terraform.tfvars

```hcl
aws_region = "us-east-1"

role_arn     = "arn:aws:iam::123456789012:role/my-role"
session_name = "terraform-session"
external_id  = "my-external-id"  # Optional

resource_name    = "my-application"
environment_name = "production"
```

### 3. Initialize and apply

```bash
terraform init
terraform plan
terraform apply
```

### 4. Verify resources created

```bash
# List Tekton Tasks
kubectl get tasks -n tekton-pipelines -l display_name=s3-management

# List StepActions
kubectl get stepactions -n tekton-pipelines -l display_name=s3-management

# Inspect StepAction (credentials are visible but not in Terraform state)
kubectl get stepaction <stepaction-name> -n tekton-pipelines -o yaml
```

## Configuration Options

### Required Fields

- `region`: AWS region (e.g., "us-east-1")
- `assume_role.role_arn`: ARN of the role to assume

Note: No `access_key` or `secret_key` needed - the pod's ambient credentials are used

### Optional Fields

- `assume_role.session_name`: Session name for auditing (default: "terraform-provider-session")
- `assume_role.external_id`: External ID for role trust policy validation
- `assume_role.duration`: Credential duration in seconds (default: 3600, min: 900, max: 43200)

## Priority: Inline Credentials vs Assume Role

If both inline credentials and assume_role are provided, **inline credentials take priority**:

```hcl
provider "facets" {
  aws {
    region     = "us-east-1"
    access_key = "inline-key"
    secret_key = "inline-secret"

    # This block will be IGNORED
    assume_role {
      role_arn = "arn:aws:iam::123456789012:role/my-role"
    }
  }
}
```

To use assume_role, provide **only** the assume_role block (no access_key or secret_key).

## How It Works

### At Terraform Apply Time

1. Provider validates configuration
2. Creates a StepAction with:
   - Role ARN, session name, external ID, duration hardcoded in the script
   - Script configured to use ambient/pod credentials for assuming the role
3. Creates a Task that references the StepAction

### At Tekton Task Runtime

1. **setup-credentials** step runs (StepAction):
   - Uses pod's ambient credentials (IRSA, instance profile, environment variables)
   - Calls `aws sts assume-role` using ambient credentials
   - Extracts temporary credentials (access key, secret key, session token)
   - Writes temporary credentials to `~/.aws/credentials`
   - Credentials expire after `duration` seconds (1-12 hours)

2. User-defined steps run:
   - Use temporary credentials automatically
   - All AWS CLI/SDK calls use the assumed role permissions

## Security Considerations

### ✅ Good

- Temporary credentials expire automatically (1-12 hours)
- Role assumptions are logged in CloudTrail
- Credentials are NOT stored in Terraform state
- Follows AWS best practices for credential rotation

### ⚠️ Considerations

- Role ARN is visible in the StepAction Kubernetes resource
- Anyone with `kubectl get stepaction` access can view the script
- Kubernetes RBAC controls access to StepActions
- Pod must have IAM permissions (IRSA, instance profile, etc.) to assume the role

## Examples Included

### 1. S3 Management (`s3_management`)

Demonstrates basic S3 operations using assumed role:
- List bucket contents
- Get bucket info
- Calculate bucket size

### 2. Cross-Account S3 Access (`cross_account_s3`)

Demonstrates cross-account S3 bucket sync:
- Access buckets in different AWS accounts
- Sync objects between buckets
- Dry-run mode for safety

## Troubleshooting

### Error: "Failed to assume role"

Check:
- Pod has IAM permissions (IRSA, instance profile) to assume the role
- Role ARN is correct
- External ID matches role trust policy (if required)
- Role trust policy allows the pod's IAM identity to assume it

### Error: "Failed to extract temporary credentials"

Check:
- StepAction image has `aws` CLI and `jq` installed
- AWS STS service is accessible from the cluster
- Pod's ambient credentials are valid and not expired

### Error: "Credentials expired"

The temporary credentials have expired. Increase the `duration` value (max 43200 seconds = 12 hours).

## Comparison: Inline vs Assume Role

| Aspect | Inline Credentials | Assume Role |
|--------|-------------------|-------------|
| **Credential Type** | Static, long-lived | Temporary, time-limited |
| **Security** | Credentials never expire | Credentials expire after duration |
| **Use Case** | Simple, same-account access | Cross-account, enhanced security |
| **Permissions** | Directly from IAM user/role | From assumed role |
| **Auditing** | Standard CloudTrail logging | Role assumption events in CloudTrail |
| **Setup Complexity** | Simple | Requires IAM role setup |
| **Runtime Overhead** | None | ~100-500ms for STS call |

## Further Reading

- [AWS STS AssumeRole Documentation](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRole.html)
- [IAM Roles for Cross-Account Access](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_common-scenarios_aws-accounts.html)
- [External ID for Third-Party Access](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_create_for-user_externalid.html)
