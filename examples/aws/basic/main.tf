terraform {
  required_providers {
    facets = {
      source  = "Facets-cloud/facets"
      version = "~> 0.1"
    }
  }
}

provider "facets" {
  aws = {
    region     = "us-west-2"
    access_key = var.aws_access_key
    secret_key = var.aws_secret_key
  }
}

# Example showing S3 sync operation with parameters
resource "facets_tekton_action_aws" "s3_sync" {
  name                 = "s3-sync"
  description          = "Sync files to S3 bucket"
  facets_resource_name = "my-application"

  facets_environment = {
    unique_name = "production"
  }

  facets_resource = {
    kind    = "application"
    flavor  = "default"
    version = "1.0"
    spec    = {}
  }

  # Define custom parameters that users will provide
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

      # Parameters are available as environment variables
      script = <<-EOT
        #!/bin/bash
        set -e

        echo "Syncing files to S3 bucket: $BUCKET_NAME"

        # AWS CLI is automatically configured via ~/.aws/credentials
        aws s3 sync "$SOURCE_PATH" "s3://$BUCKET_NAME/" --delete

        echo "✓ Sync completed successfully"
      EOT
    }
  ]
}

output "task_name" {
  value = facets_tekton_action_aws.s3_sync.task_name
}

output "step_action_name" {
  value = facets_tekton_action_aws.s3_sync.step_action_name
}
