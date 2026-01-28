# facets_tekton_action_aws

Manages a Tekton Task and StepAction for AWS-based workflows in Facets.

This resource automatically creates:
- A Tekton Task with your specified workflow steps
- A StepAction that sets up AWS credentials using IRSA role chaining

## How It Works

The `facets_tekton_action_aws` resource provides automatic AWS credential management through **IRSA (IAM Roles for Service Accounts)** with native AWS SDK role chaining.

### IRSA Role Chaining Flow

1. **Configuration**: Pod's ServiceAccount has an IRSA role with `sts:AssumeRole` permission
2. **Setup Step**: Creates AWS config file with `source_profile` pointing to IRSA credentials
3. **User Steps**: AWS SDK automatically:
   - Reads IRSA credentials from environment variables
   - Uses them to call `sts:AssumeRole` on the target role
   - Caches and auto-refreshes temporary credentials
   - All transparent to user workflows

### Authentication Flow

```
Pod IRSA Role → source_profile → Target Role (with session_name)
```

**Key Features**:
- **Silent setup**: No debug output, production-ready
- **Automatic credential refresh**: AWS SDK handles token lifecycle
- **Session tracking**: Configurable session names for CloudTrail
- **Cross-account access**: Securely assume roles in different AWS accounts
- **External ID support**: Enhanced security for cross-account scenarios

### Architecture Details

- **No Terraform State Exposure**: AWS configuration is NOT stored in Terraform state. Only Task and StepAction names are tracked.
- **Credential Location**: AWS config is written to `/workspace/.aws/config` at runtime
- **No skip-containers needed**: All containers receive the same IRSA credentials
- **Native SDK behavior**: Uses standard AWS SDK credential chain

## Provider Configuration

Configure the provider with AWS assume_role settings:

```hcl
provider "facets" {
  aws = {
    region = "us-east-1"
    assume_role = {
      role_arn     = "arn:aws:iam::123456789012:role/TargetRole"
      session_name = "my-workflow"       # Optional (auto-generated if not provided)
      external_id  = "unique-external-id"  # Optional
    }
  }
}
```

**Requirements**:
- ServiceAccount must have IRSA role configured
- IRSA role must have `sts:AssumeRole` permission on the target role
- Target role must trust the IRSA role (via trust policy)

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `CLUSTER_ID` | No | `"na"` | Cluster identifier added to resource labels for tracking |

## Example Usage

### Basic S3 Operations

```hcl
provider "facets" {
  aws = {
    region = "us-east-1"
    assume_role = {
      role_arn     = "arn:aws:iam::123456789012:role/S3AccessRole"
      session_name = "s3-sync-workflow"
    }
  }
}

resource "facets_tekton_action_aws" "s3_sync" {
  name                 = "s3-sync"
  description          = "Sync files to S3 bucket"
  facets_resource_name = "my-application"

  facets_environment = {
    unique_name = "production"
  }

  facets_resource = {
    kind    = "application"
    flavor  = "aws"
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
        # AWS credentials automatically available via IRSA + source_profile
        echo "Syncing files to S3..."
        aws s3 sync $(params.SOURCE_PATH) s3://$(params.BUCKET_NAME)/ --delete
        echo "Sync completed successfully"
      EOT
    }
  ]
}
```

### Cross-Account Access

```hcl
provider "facets" {
  aws = {
    region = "us-west-2"
    assume_role = {
      role_arn     = "arn:aws:iam::987654321098:role/CrossAccountRole"
      session_name = "cross-account-operations"
      external_id  = "my-secure-external-id"
    }
  }
}

resource "facets_tekton_action_aws" "cross_account_backup" {
  name                 = "cross-account-backup"
  description          = "Backup data to cross-account S3"
  facets_resource_name = "data-service"

  facets_environment = {
    unique_name = "production"
  }

  facets_resource = {
    kind    = "service"
    flavor  = "aws"
    version = "1.0"
    spec    = {}
  }

  params = [
    {
      name = "DATA_FILE"
      type = "string"
    },
    {
      name = "BACKUP_BUCKET"
      type = "string"
    }
  ]

  steps = [
    {
      name  = "backup-data"
      image = "amazon/aws-cli:latest"

      script = <<-EOT
        #!/bin/bash
        set -e

        # Verify we're using the target account
        echo "Current identity:"
        aws sts get-caller-identity

        # Perform backup
        echo "Backing up data to cross-account bucket..."
        aws s3 cp $(params.DATA_FILE) s3://$(params.BACKUP_BUCKET)/backups/

        echo "Backup completed successfully"
      EOT
    }
  ]
}
```

### Multiple Steps with EC2 Operations

```hcl
resource "facets_tekton_action_aws" "ec2_workflow" {
  name                 = "ec2-restart-workflow"
  description          = "Stop and start EC2 instance"
  facets_resource_name = "compute-service"

  facets_environment = {
    unique_name = "staging"
  }

  facets_resource = {
    kind    = "service"
    flavor  = "aws"
    version = "1.0"
    spec    = {}
  }

  params = [
    {
      name = "INSTANCE_ID"
      type = "string"
    }
  ]

  steps = [
    {
      name  = "stop-instance"
      image = "amazon/aws-cli:latest"

      script = <<-EOT
        #!/bin/bash
        set -e
        echo "Stopping EC2 instance: $(params.INSTANCE_ID)"
        aws ec2 stop-instances --instance-ids $(params.INSTANCE_ID)

        # Wait for stopped state
        aws ec2 wait instance-stopped --instance-ids $(params.INSTANCE_ID)
        echo "Instance stopped"
      EOT
    },
    {
      name  = "start-instance"
      image = "amazon/aws-cli:latest"

      script = <<-EOT
        #!/bin/bash
        set -e
        echo "Starting EC2 instance: $(params.INSTANCE_ID)"
        aws ec2 start-instances --instance-ids $(params.INSTANCE_ID)

        # Wait for running state
        aws ec2 wait instance-running --instance-ids $(params.INSTANCE_ID)
        echo "Instance started successfully"
      EOT
    }
  ]
}
```

### With Resource Limits and Environment Variables

```hcl
resource "facets_tekton_action_aws" "data_processing" {
  name                 = "process-and-upload"
  description          = "Process data and upload to S3"
  facets_resource_name = "data-pipeline"

  facets_environment = {
    unique_name = "production"
  }

  facets_resource = {
    kind    = "pipeline"
    flavor  = "aws"
    version = "1.0"
    spec    = {}
  }

  steps = [
    {
      name  = "process-data"
      image = "python:3.9-slim"

      env = [
        {
          name  = "DATA_SOURCE"
          value = "/workspace/input"
        },
        {
          name  = "PROCESSING_MODE"
          value = "batch"
        }
      ]

      resources = {
        requests = {
          cpu    = "1000m"
          memory = "2Gi"
        }
        limits = {
          cpu    = "2000m"
          memory = "4Gi"
        }
      }

      script = <<-EOT
        #!/bin/bash
        set -e
        echo "Processing data from $DATA_SOURCE in $PROCESSING_MODE mode..."
        # Data processing logic here
        echo "Processing complete"
      EOT
    },
    {
      name  = "upload-results"
      image = "amazon/aws-cli:latest"

      env = [
        {
          name  = "OUTPUT_BUCKET"
          value = "my-results-bucket"
        }
      ]

      script = <<-EOT
        #!/bin/bash
        set -e
        echo "Uploading results to S3..."
        aws s3 cp /workspace/output/ s3://$OUTPUT_BUCKET/results/ --recursive
        echo "Upload complete"
      EOT
    }
  ]
}
```

## Argument Reference

### Required Arguments

* `name` - (String) Display name of the Tekton Task
* `facets_resource_name` - (String) Resource name as defined in the Facets blueprint
* `facets_environment` - (Object) Facets-managed environment configuration:
  * `unique_name` - (String) Unique name of the Facets-managed environment
* `facets_resource` - (Object) Resource definition from Facets blueprint:
  * `kind` - (String) Resource kind (e.g., "application", "service")
  * `flavor` - (String) Resource flavor (e.g., "aws", "k8s")
  * `version` - (String) Resource version
  * `spec` - (Dynamic) Additional resource specifications (can be empty object)
* `steps` - (List of Objects) List of workflow steps to execute:
  * `name` - (String) Step name
  * `image` - (String) Container image (should include AWS CLI/SDK for AWS operations)
  * `script` - (String) Script to execute in the step

### Optional Arguments

* `description` - (String) Description of the Tekton Task
* `namespace` - (String) Kubernetes namespace for Tekton resources. Defaults to `"tekton-pipelines"`
* `params` - (List of Objects) List of custom parameters for the Tekton Task:
  * `name` - (String) Parameter name
  * `type` - (String) Parameter type (e.g., "string", "array")

### Optional Step Arguments

* `resources` - (Object) Compute resources for the step:
  * `requests` - (Map of Strings) Minimum compute resources (e.g., `{cpu = "100m", memory = "128Mi"}`)
  * `limits` - (Map of Strings) Maximum compute resources (e.g., `{cpu = "500m", memory = "512Mi"}`)
* `env` - (List of Objects) Environment variables for the step:
  * `name` - (String) Environment variable name
  * `value` - (String) Environment variable value

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - Resource identifier in format `namespace/task_name`
* `task_name` - Generated Tekton Task name (hash-based, may be truncated to 63 characters)
* `step_action_name` - Generated StepAction name for AWS credential setup

## AWS Configuration Generated

The setup-credentials StepAction creates the following AWS config file at runtime:

```ini
[profile irsa]
web_identity_token_file = /var/run/secrets/eks.amazonaws.com/serviceaccount/token
role_arn = <POD_IRSA_ROLE_ARN>

[default]
source_profile = irsa
role_arn = <TARGET_ROLE_ARN>
role_session_name = <SESSION_NAME>
region = <REGION>
external_id = <EXTERNAL_ID>  # If provided
```

This configuration enables AWS SDK to automatically:
1. Use IRSA credentials to authenticate as the pod's IAM role
2. Assume the target role using those credentials
3. Use the target role's temporary credentials for all AWS operations

## IAM Requirements

### Control Plane IRSA Role

The pod's IRSA role must have permission to assume the target role:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": "sts:AssumeRole",
    "Resource": "arn:aws:iam::TARGET_ACCOUNT:role/TargetRole"
  }]
}
```

### Target Role Trust Policy

The target role must trust the IRSA role:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {
      "AWS": "arn:aws:iam::CONTROL_PLANE_ACCOUNT:role/IRSARole"
    },
    "Action": "sts:AssumeRole",
    "Condition": {
      "StringEquals": {
        "sts:ExternalId": "my-external-id"  // Optional
      }
    }
  }]
}
```

## Session Names

Session names appear in CloudTrail logs and help track who/what assumed the role:

- **Explicit**: Set `session_name` in provider configuration
- **Auto-generated**: If not provided, generates random name like `terraform-a1b2c3d4e5f6789`

Session names appear in the assumed role ARN:
```
arn:aws:sts::123456789012:assumed-role/TargetRole/my-session-name
```

## Comparison with Kubernetes Action

| Aspect | Kubernetes Action | AWS Action |
|--------|-------------------|------------|
| **Credential Setup** | Decodes kubeconfig parameter | Creates AWS config with role chaining |
| **Credential Source** | Facets UI injects at runtime | Pod IRSA + AWS STS |
| **Runtime Parameters** | FACETS_USER_KUBECONFIG | None |
| **Credential Type** | User-specific kubeconfig | Temporary credentials (auto-refreshed) |
| **Cross-Account** | N/A | Yes |
| **State Exposure** | No | No |

## Import

Tekton AWS actions can be imported using the format `namespace/task_name`:

```shell
terraform import facets_tekton_action_aws.example tekton-pipelines/a1b2c3d4e5f6789012345678901234567890abcd
```

To find the task name:

```shell
kubectl get tasks -n tekton-pipelines -l display_name=your-action-name
```

## Troubleshooting

### Role Assumption Failures

Check the user step logs (setup step is silent):

```bash
# Find the TaskRun
kubectl get taskruns -n tekton-pipelines

# Check user step logs
kubectl logs -n tekton-pipelines <taskrun-name> -c step-<step-name>
```

Common issues:
- **AccessDenied**: IRSA role doesn't have `sts:AssumeRole` permission
- **Not authorized**: Target role's trust policy doesn't allow IRSA role
- **External ID mismatch**: Check external_id in both provider config and trust policy

### IRSA Not Configured

If AWS commands fail with credentials error:

1. Verify ServiceAccount has IRSA annotation:
   ```bash
   kubectl get sa <service-account-name> -n <namespace> -o yaml
   ```
   Should have: `eks.amazonaws.com/role-arn: arn:aws:iam::...`

2. Check pod environment variables:
   ```bash
   kubectl get pod <pod-name> -o jsonpath='{.spec.containers[0].env}'
   ```
   Should include: `AWS_ROLE_ARN` and `AWS_WEB_IDENTITY_TOKEN_FILE`

### Debug AWS Configuration

Add debug commands to your step script:

```bash
# Check AWS config file
cat /workspace/.aws/config

# Check environment variables
env | grep AWS

# Test AWS SDK credential chain
aws sts get-caller-identity
```

## Examples

For complete working examples, see:
- [Cross-Account IAM Role Assumption](../../examples/aws/assume-role/)
