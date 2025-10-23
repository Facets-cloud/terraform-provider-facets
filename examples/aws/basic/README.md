# Basic AWS Action Example

This example demonstrates how to create a basic AWS action that syncs files to an S3 bucket.

## Prerequisites

- Terraform >= 1.0
- Access to a Kubernetes cluster with Tekton installed
- AWS credentials (access key and secret key)
- kubectl configured to access your cluster

## Usage

1. Create a `terraform.tfvars` file with your AWS credentials:

```hcl
aws_access_key = "your-access-key-here"
aws_secret_key = "your-secret-key-here"
```

2. Initialize Terraform:

```bash
terraform init
```

3. Plan the changes:

```bash
terraform plan
```

4. Apply the configuration:

```bash
terraform apply
```

## What This Creates

This example creates two Tekton resources in the `tekton-pipelines` namespace:

1. **StepAction**: `setup-aws-credentials-{hash}` - Configures AWS credentials
2. **Task**: `{hash}` - Main task with your S3 sync logic

The Task includes:
- **Auto-injected parameters**: `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`
- **User-defined parameters**: `BUCKET_NAME`, `SOURCE_PATH`
- **Auto-injected env vars** (in all steps):
  - `AWS_CONFIG_FILE=/workspace/.aws/config`
  - `AWS_SHARED_CREDENTIALS_FILE=/workspace/.aws/credentials`

## Triggering the Action

When this action is triggered from the Facets UI, you'll be prompted to provide:
- `BUCKET_NAME`: The S3 bucket to sync to
- `SOURCE_PATH`: The local path to sync from

The Facets backend will automatically inject the AWS credentials from the provider configuration.

## Verification

After applying, you can verify the resources were created:

```bash
# Check if Task was created
kubectl get tasks -n tekton-pipelines -l display_name=s3-sync

# Check if StepAction was created
kubectl get stepactions -n tekton-pipelines -l display_name=s3-sync

# View Task details
kubectl describe task <task-name> -n tekton-pipelines
```

## Cleanup

To remove the resources:

```bash
terraform destroy
```
