# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.2.0] - 2026-02-02

### Changed
- **Refactored Kubernetes client initialization** to create fresh client per operation
  - Matches terraform-provider-helm best practices for client management
  - No stale client issues - fresh client ensures latest config/credentials
  - Thread-safe - no shared mutable state between operations
  - `terraform validate` and `terraform plan` now succeed without kubeconfig
  - Client errors only occur during actual CRUD operations (`terraform apply`)

### Fixed
- Added nil pointer protection for provider data in AWS resource
  - Prevents panic if provider block is misconfigured
  - Returns clear error message instead of crashing

### Technical Details
This is an internal refactoring with no schema changes. User configurations remain unchanged.
The provider now defers all client creation and validation to CRUD operations, allowing
CI pipelines to validate Terraform configurations without requiring Kubernetes credentials.

## [1.1.1] - 2026-02-02

### Documentation
- Clarify ServiceAccount requirements in `facets_tekton_action_aws` documentation
  - Rewrite "How It Works" section to match Facets-specific style
  - Specify that TaskRuns use `facets-workflows-sa` ServiceAccount in `tekton-pipelines` namespace
  - Simplify Prerequisites to focus on IRSA requirements

## [1.1.0] - 2026-02-02

### Added
- **New Resource: `facets_tekton_action_aws`** for AWS workflow automation
  - IRSA-only authentication with native AWS SDK role chaining via `source_profile`
  - Session name support (configurable or auto-generated) for CloudTrail tracking
  - Cross-account access with secure temporary credentials
  - External ID support for enhanced security
  - Full CRUD operations with import support
- **`cloud_action` label** added to all Tekton Task and StepAction resources
  - `cloud_action=true` for `facets_tekton_action_aws` resources
  - `cloud_action=false` for `facets_tekton_action_kubernetes` resources

### Changed
- **Refactored shared Tekton logic** into reusable `internal/provider/tekton/` package
  - ~70% code duplication eliminated between AWS and Kubernetes actions
  - Unified naming convention for both resource types

### Documentation
- Comprehensive documentation for `facets_tekton_action_aws` at `docs/resources/tekton_action_aws.md`
- Complete working example at `examples/aws/assume-role/`
- Updated README.md with AWS action schema

### Breaking Changes
- `facets_tekton_action_aws` requires IRSA-only authentication (no inline credentials)
- `assume_role` block is required in provider configuration for AWS actions
- Service account must have IRSA role with `sts:AssumeRole` permission

## [1.0.0] - 2026-01-14

### Added
- **Custom Labels Support**: Add optional `labels` attribute to `facets_tekton_action_kubernetes` resource
  - Allows users to add custom Kubernetes labels to Tekton Task and StepAction resources
  - Auto-generated labels (display_name, resource_name, resource_kind, environment_unique_name, cluster_id) take precedence over custom labels
  - Enables better organization and tracking of resources

### Documentation
- Add comprehensive local testing guide
- Update resource documentation with custom labels usage examples

## [0.1.4] - 2024-01-XX

### Fixed
- Corrected `facets_resource` schema for `facets_tekton_action_kubernetes` resource
  - Removed unused `flavor`, `version`, and `spec` fields from schema
  - Only `kind` field is tracked in state (used in resource labels)
  - Other fields can still be provided in configuration but are silently ignored
- Prevents unnecessary plan changes when modifying unused metadata fields

### Migration
No action required. Existing configurations continue to work without changes.
Users can provide `flavor`, `version`, and `spec` fields, but they will be silently ignored.
Only the `kind` field is used by the provider.

### Technical Details
This change leverages Terraform's behavior where unknown attributes in nested objects
are silently ignored. The provider now only tracks the `kind` field in state, which is
the only field actually used in resource labels. Changes to other fields like `flavor`,
`version`, or `spec` will not appear in terraform plan or trigger any updates.

## [0.1.3] - 2024-XX-XX

### Added
- Initial release with `facets_tekton_action_kubernetes` resource
- Initial release with `facets_tekton_action_aws` resource
- Support for Kubernetes-based Tekton workflows
- Support for AWS-based Tekton workflows with AssumeRole
- Automatic credential injection for both Kubernetes and AWS actions
