# Multiple Steps Example

This example demonstrates a complete CI/CD pipeline with multiple sequential steps.

## What This Does

Creates a 4-step deployment pipeline:
1. **Validate** - Dry-run Kubernetes manifests
2. **Deploy** - Apply manifests to cluster
3. **Verify** - Wait for deployment rollout
4. **Smoke Test** - Run basic health checks

## Features Demonstrated

- Multiple sequential steps
- Different container images per step
- Environment variables in steps
- Resource limits on specific steps
- Complete CI/CD workflow pattern

## Step Execution

Steps execute in the order defined:

```
setup-credentials (automatic)
    ↓
validate
    ↓
deploy
    ↓
verify
    ↓
smoke-test
```

If any step fails, the workflow stops and subsequent steps don't execute.

## Kubernetes Access

All steps have access to kubectl because:
- The `setup-credentials` step (automatically added) configures kubeconfig
- All steps inherit the `KUBECONFIG` environment variable
- User's RBAC permissions apply to all kubectl commands

## Customization

You can modify this pipeline by:
- Adding more steps (e.g., tests, notifications)
- Changing container images for specific tools
- Adding parameters for dynamic behavior
- Including environment-specific configurations
