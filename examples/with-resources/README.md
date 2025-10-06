# Example with Resource Limits

This example demonstrates how to specify CPU and memory resource constraints for workflow steps.

## What This Does

Creates a deployment action with specified resource requests and limits to control resource consumption.

## Features Demonstrated

- Resource requests (guaranteed resources)
- Resource limits (maximum allowed resources)
- Using kubectl in workflow steps
- Kubernetes access via auto-injected credentials

## Resource Configuration

```hcl
resources = {
  requests = {
    cpu    = "100m"      # Guaranteed: 0.1 CPU cores
    memory = "128Mi"     # Guaranteed: 128 MiB RAM
  }
  limits = {
    cpu    = "500m"      # Maximum: 0.5 CPU cores
    memory = "512Mi"     # Maximum: 512 MiB RAM
  }
}
```

## Why Use Resource Limits?

- **Requests**: Ensures your workflow gets minimum guaranteed resources
- **Limits**: Prevents workflows from consuming excessive cluster resources
- **Scheduling**: Helps Kubernetes schedule pods efficiently
