# Basic Example

This example demonstrates the simplest possible Tekton action using the Facets provider.

## What This Does

Creates a Tekton Task with a single step that prints a hello world message.

## Usage

```bash
terraform init
terraform plan
terraform apply
```

## Outputs

- `task_name` - The generated Tekton Task name (hash-based identifier)

## Features Demonstrated

- Minimal required configuration
- Single-step workflow
- Basic script execution
