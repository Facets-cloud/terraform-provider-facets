# facets_tekton_action_aws

Manages a Tekton Task and StepAction for AWS-based workflows in Facets.

This resource automatically creates:
- A Tekton Task with your specified workflow steps
- A StepAction that sets up AWS credentials automatically

## How It Works

The `facets_tekton_action_aws` resource provides automatic AWS credential management through two authentication methods:

### Inline Credentials (Static)

When you configure the provider with inline AWS credentials:

1. **Credential Embedding**: Static AWS credentials (access key, secret key) are embedded directly into the StepAction script at Terraform apply time
2. **Runtime Execution**: When triggered via Facets UI:
   - The StepAction runs and creates `~/.aws/credentials` and `~/.aws/config` files
   - AWS environment variables (`AWS_CONFIG_FILE`, `AWS_SHARED_CREDENTIALS_FILE`) are automatically injected into all user steps
   - Your workflow steps execute with AWS CLI/SDK access via credential files
3. **IRSA Isolation**: If running in EKS with IRSA enabled, the Task includes a `skip-containers` annotation to prevent IRSA injection into user steps, ensuring they use the credential files instead

**Use Case**: Simple, same-account workflows with static credentials

### IAM Role Assumption (Temporary Credentials)

When you configure the provider with `assume_role` (no inline credentials):

1. **Ambient Credentials**: The StepAction uses the pod's ambient IRSA credentials (from ServiceAccount)
2. **Runtime Role Assumption**: When triggered via Facets UI:
   - The StepAction container has IRSA credentials (not in skip list)
   - It calls `aws sts assume-role` using the pod's IRSA credentials
   - Assumes the target role (can be in a different AWS account)
   - Extracts temporary credentials (access key, secret key, session token) using `jq`
   - Writes temporary credentials to `~/.aws/credentials` and `~/.aws/config` files
3. **IRSA Isolation**: User step containers are excluded from IRSA injection via `skip-containers` annotation
   - User steps do NOT have IRSA environment variables
   - AWS SDK skips IRSA and uses credential files instead
   - Commands execute with the assumed role's temporary credentials
4. **Credential Expiration**: Temporary credentials expire after the configured duration (15 minutes to 12 hours)

**Use Case**: Cross-account access, enhanced security, temporary credentials, audit trails

### Key Architecture Details

- **No Terraform State Exposure**: AWS credentials are NOT stored in Terraform state. Only the Task and StepAction names are tracked.
- **Credential Location**: Credentials are embedded in the StepAction (visible via `kubectl get stepaction -o yaml` but not in state)
- **Annotation Propagation**: The `eks.amazonaws.com/skip-containers` annotation is added to Task metadata and automatically propagates through Tekton: Task → TaskRun → Pod
- **Container Naming**: User steps are named `step-user-step-1`, `step-user-step-2`, etc., and these names are listed in the skip-containers annotation
- **IRSA Webhook Behavior**: The EKS IRSA webhook reads the skip-containers annotation and:
  - Injects IRSA credentials into the StepAction container (needed for AssumeRole)
  - Skips IRSA injection for user step containers (they use credential files)

## Provider Configuration

Configure the provider with AWS credentials:

### Inline Credentials

```hcl
provider "facets" {
  aws = {
    region     = "us-east-1"
    access_key = var.aws_access_key  # Sensitive
    secret_key = var.aws_secret_key  # Sensitive
  }
}
```

### IAM Role Assumption (with Ambient Credentials)

```hcl
provider "facets" {
  aws = {
    region = "us-east-1"
    assume_role = {
      role_arn     = "arn:aws:iam::123456789012:role/TargetRole"
      session_name = "facets-tekton-session"
      external_id  = "unique-external-id"  # Optional
      duration     = 3600                   # Optional, 900-43200 seconds
    }
  }
}
```

**Important**: For assume_role, do NOT provide `access_key` or `secret_key`. The StepAction will use the pod's ambient IRSA credentials.

### Priority Logic

If both inline credentials AND assume_role are provided, inline credentials take priority and assume_role is ignored.

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `CLUSTER_ID` | No | `"na"` | Cluster identifier added to resource labels for tracking |

## Example Usage

### Basic S3 Sync (Inline Credentials)

```hcl
resource "facets_tekton_action_aws" "s3_sync" {
  name                 = "s3-sync"
  description          = "Sync files to S3 bucket"
  facets_resource_name = "my-application"

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
      name = "BUCKET_NAME"
      type = "string"
    },
    {
      name = "SOURCE_PATH"
      type = "string"
    }
  ]

  steps = [
    {
      name  = "sync-to-s3"
      image = "amazon/aws-cli:latest"

      script = <<-EOT
        #!/bin/bash
        set -e
        echo "Syncing files to S3..."
        aws s3 sync $SOURCE_PATH s3://$BUCKET_NAME/ --delete
        echo "Sync completed successfully"
      EOT
    }
  ]
}
```

### Cross-Account Access (AssumeRole)

```hcl
provider "facets" {
  aws = {
    region = "us-east-1"
    assume_role = {
      role_arn     = "arn:aws:iam::987654321098:role/CrossAccountS3Role"
      session_name = "cross-account-s3-sync"
      external_id  = "my-secure-external-id"
      duration     = 3600
    }
  }
}

resource "facets_tekton_action_aws" "cross_account_sync" {
  name                 = "cross-account-s3-sync"
  description          = "Sync S3 buckets across accounts"
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
      name = "SOURCE_BUCKET"
      type = "string"
    },
    {
      name = "DEST_BUCKET"
      type = "string"
    }
  ]

  steps = [
    {
      name  = "sync-buckets"
      image = "amazon/aws-cli:latest"

      script = <<-EOT
        #!/bin/bash
        set -e
        echo "Syncing from source to destination bucket..."
        aws s3 sync s3://$SOURCE_BUCKET/ s3://$DEST_BUCKET/ --delete
        echo "Cross-account sync completed"
      EOT
    }
  ]
}
```

### Multiple Steps with Environment Variables

```hcl
resource "facets_tekton_action_aws" "backup_workflow" {
  name                 = "backup-and-cleanup"
  facets_resource_name = "data-service"

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
      name  = "backup-database"
      image = "amazon/aws-cli:latest"

      env = [
        {
          name  = "BACKUP_BUCKET"
          value = "my-backups"
        }
      ]

      script = <<-EOT
        #!/bin/bash
        set -e
        BACKUP_FILE="backup-$(date +%Y%m%d-%H%M%S).sql"
        echo "Creating backup: $BACKUP_FILE"
        # Backup logic here
        aws s3 cp "$BACKUP_FILE" "s3://$BACKUP_BUCKET/database/"
      EOT
    },
    {
      name  = "cleanup-old-backups"
      image = "amazon/aws-cli:latest"

      env = [
        {
          name  = "BACKUP_BUCKET"
          value = "my-backups"
        },
        {
          name  = "RETENTION_DAYS"
          value = "30"
        }
      ]

      script = <<-EOT
        #!/bin/bash
        set -e
        echo "Cleaning up backups older than $RETENTION_DAYS days..."
        CUTOFF_DATE=$(date -d "$RETENTION_DAYS days ago" +%Y-%m-%d)
        aws s3 ls "s3://$BACKUP_BUCKET/database/" | while read -r line; do
          FILE_DATE=$(echo $line | awk '{print $1}')
          if [[ "$FILE_DATE" < "$CUTOFF_DATE" ]]; then
            FILE_NAME=$(echo $line | awk '{print $4}')
            aws s3 rm "s3://$BACKUP_BUCKET/database/$FILE_NAME"
            echo "Deleted: $FILE_NAME"
          fi
        done
      EOT
    }
  ]
}
```

### With Resource Limits

```hcl
resource "facets_tekton_action_aws" "large_upload" {
  name                 = "large-file-upload"
  facets_resource_name = "data-pipeline"

  facets_environment = {
    unique_name = "production"
  }

  facets_resource = {
    kind    = "pipeline"
    flavor  = "k8s"
    version = "1.0"
    spec    = {}
  }

  steps = [
    {
      name  = "upload-data"
      image = "amazon/aws-cli:latest"

      resources = {
        requests = {
          cpu    = "500m"
          memory = "1Gi"
        }
        limits = {
          cpu    = "2000m"
          memory = "4Gi"
        }
      }

      script = <<-EOT
        #!/bin/bash
        set -e
        echo "Uploading large dataset..."
        aws s3 cp large-dataset.tar.gz s3://my-bucket/datasets/ --storage-class GLACIER
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
  * `image` - (String) Container image for the step (should include AWS CLI for AWS operations)
  * `script` - (String) Script to execute in the step

### Optional Arguments

* `description` - (String) Description of the Tekton Task
* `namespace` - (String) Kubernetes namespace for Tekton resources. Defaults to `"tekton-pipelines"`
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
* `step_action_name` - Generated StepAction name for AWS credential setup (computed from hash). This StepAction automatically configures AWS access for the workflow steps.

## Auto-Injected Environment Variables

The following environment variables are automatically injected into all user steps:

* `AWS_CONFIG_FILE` - Path to AWS config file (`/workspace/.aws/config`)
* `AWS_SHARED_CREDENTIALS_FILE` - Path to AWS credentials file (`/workspace/.aws/credentials`)

These ensure that the AWS CLI and SDK automatically use the credential files created by the StepAction.

## Authentication Methods

### Inline Credentials

**When to use:**
- Simple, same-account workflows
- When you have static AWS credentials
- Quick setup without IAM role configuration

**Security considerations:**
- Credentials are embedded in StepAction (visible via kubectl)
- Not stored in Terraform state
- Long-lived credentials (no automatic expiration)
- Access controlled by Kubernetes RBAC

### IAM Role Assumption

**When to use:**
- Cross-account access required
- Enhanced security with temporary credentials
- Audit trails via CloudTrail AssumeRole events
- When pod already has IRSA configured

**Security considerations:**
- Uses temporary credentials (expire after duration)
- No static credentials stored
- Requires pod to have IRSA with AssumeRole permissions
- Supports external ID for additional security

**Requirements:**
- Container image must include AWS CLI v2 and `jq`
- Pod must have IRSA configured with AssumeRole permissions
- Target role must trust the pod's IRSA role

**Example IAM trust policy for target role:**
```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "AWS": "arn:aws:iam::111111111111:role/PodIRSARole"
      },
      "Action": "sts:AssumeRole",
      "Condition": {
        "StringEquals": {
          "sts:ExternalId": "my-secure-external-id"
        }
      }
    }
  ]
}
```

## Comparison with Kubernetes Action

| Aspect | Kubernetes Action | AWS Action (Inline) | AWS Action (AssumeRole) |
|--------|------------------|---------------------|-------------------------|
| **Credential Setup** | Decodes kubeconfig parameter | Creates AWS credential files (static) | Assumes role → creates temp credential files |
| **Credential Source** | Facets UI injects at runtime | Embedded in StepAction | Pod IRSA + AWS STS |
| **Runtime Parameters** | FACETS_USER_KUBECONFIG | None | None |
| **Credential Type** | User-specific kubeconfig | Static AWS credentials | Temporary credentials (STS) |
| **Expiration** | Based on kubeconfig | Never (unless rotated) | After duration (15min-12hr) |
| **Cross-Account** | N/A | No | Yes |
| **Container Isolation** | None | Via skip-containers annotation | Via skip-containers annotation |
| **State Exposure** | No | No | No |

## Import

Tekton AWS actions can be imported using the format `namespace/task_name`:

```shell
terraform import facets_tekton_action_aws.example tekton-pipelines/a1b2c3d4e5f6789012345678901234567890abcd
```

Note: The task name is a hash-based identifier. You can find it by running:

```shell
kubectl get tasks -n tekton-pipelines -l display_name=your-action-name
```

## Troubleshooting

### AssumeRole Issues

If AssumeRole fails, check the StepAction logs for detailed output:

```bash
# Find the TaskRun
kubectl get taskruns -n tekton-pipelines

# Check logs for the setup-credentials step
kubectl logs -n tekton-pipelines <taskrun-name> -c step-setup-credentials
```

The logs include:
- Full AWS STS AssumeRole response
- Credential extraction details
- Any errors from AWS CLI

### IRSA Not Working

If user steps are using pod IRSA instead of credential files:

1. Verify skip-containers annotation exists on Task:
   ```bash
   kubectl get task <task-name> -n tekton-pipelines -o jsonpath='{.metadata.annotations}'
   ```

2. Verify annotation propagated to Pod:
   ```bash
   kubectl get pod <pod-name> -o jsonpath='{.metadata.annotations}'
   ```

3. Check that user step container names match annotation:
   ```bash
   kubectl get pod <pod-name> -o jsonpath='{.spec.containers[*].name}'
   ```

### Credential File Issues

Check that AWS credential files are created correctly:

```bash
# In a user step, add debug commands:
ls -la /workspace/.aws/
cat /workspace/.aws/credentials
cat /workspace/.aws/config
```

## Examples

For complete working examples, see:
- [Basic S3 Sync (Inline Credentials)](../../examples/aws/basic/)
- [IAM Role Assumption (Cross-Account)](../../examples/aws/assume-role/)
