# Example with Custom Parameters

This example demonstrates how to define custom parameters that users can provide when triggering the action.

## What This Does

Creates a scaling action that accepts parameters for deployment name, replica count, and namespace.

## Features Demonstrated

- Custom parameter definition
- Using parameters in scripts
- Dynamic workflow behavior based on user input

## Parameters

When users trigger this action via the Facets UI, they'll be prompted to provide:

- `DEPLOYMENT_NAME` - Name of the deployment to scale
- `REPLICAS` - Target number of replicas
- `NAMESPACE` - Kubernetes namespace

## Usage Example

When triggered, users might provide:
```
DEPLOYMENT_NAME=my-app
REPLICAS=5
NAMESPACE=production
```

The workflow will then scale the `my-app` deployment to 5 replicas in the `production` namespace.

## Auto-Injected Parameters

In addition to custom parameters, these are automatically provided:
- `FACETS_USER_EMAIL` - Email of the user triggering the action
- `FACETS_USER_KUBECONFIG` - Base64-encoded kubeconfig with RBAC permissions
