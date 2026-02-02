# facets_tekton_action_aws

Manages a Tekton Task and StepAction for AWS-based workflows in Facets. This resource automatically sets up AWS credentials using IRSA role chaining, allowing your workflow steps to access AWS services securely.

## How It Works

When a user triggers this action via the Facets UI:

1. **ServiceAccount with IRSA**: The TaskRun executes using the `facets-workflows-sa` ServiceAccount in the `tekton-pipelines` namespace, which has an IRSA role attached via the `eks.amazonaws.com/role-arn` annotation
2. **Automatic Credential Setup**: A `setup-aws-credentials` step is automatically prepended to your workflow that:
   - Uses the IRSA credentials to assume the target role (configured in your provider's `assume_role` block)
   - Configures AWS SDK environment variables for all subsequent steps
3. **Your Steps Run**: Your defined workflow steps execute with the assumed role's AWS permissions

This enables secure cross-account access without embedding long-lived credentials.

## Prerequisites

The `facets-workflows-sa` ServiceAccount in the `tekton-pipelines` namespace must be configured with IRSA:

- **IRSA role**: The ServiceAccount must have an `eks.amazonaws.com/role-arn` annotation pointing to an IAM role
- **AssumeRole permission**: The IRSA role must have `sts:AssumeRole` permission on the target role specified in your provider configuration
- **Trust policy**: The target role must trust the IRSA role

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

### Check TaskRun Logs

```bash
# Find the TaskRun
kubectl get taskruns -n tekton-pipelines

# Check step logs
kubectl logs -n tekton-pipelines <taskrun-name> -c step-<step-name>
```

### Common Issues

- **AccessDenied**: IRSA role doesn't have `sts:AssumeRole` permission on the target role
- **Not authorized**: Target role's trust policy doesn't allow the IRSA role
- **External ID mismatch**: Verify `external_id` matches in both provider config and target role trust policy
- **Credentials error**: Verify ServiceAccount has IRSA annotation (`eks.amazonaws.com/role-arn`)

### Debug Commands

Add these to your step script to troubleshoot:

```bash
# Check current identity
aws sts get-caller-identity

# Check environment variables
env | grep AWS
```
