# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **`facets_tekton_action_azure`** — first-class Azure action type, so Azure actions get the same one-click experience AWS actions already have. Credentials are configured once in the provider `azure` block and injected by a prepended `setup-credentials` step; the user triggering the action never supplies them.
  - **OIDC federation mode** (`use_oidc_federation = true`) — Microsoft Entra ID exchanges the pod's projected service-account token for an Azure token, so **no client secret exists anywhere**. The Azure analogue of the AWS variant's IRSA flow, and it works cross-cloud: a pod in an EKS control-plane cluster can authenticate to Azure. Requires a federated identity credential trusting the cluster's OIDC issuer with audience `api://AzureADTokenExchange`.
  - **Client secret mode** — read at pod start from the `facets-azure-credentials` Kubernetes Secret via `secretKeyRef`, so the value does not appear in the rendered Task manifest.
  - Setting both `client_secret` and `use_oidc_federation`, or neither, is rejected with a clear diagnostic.
  - Injects `AZURE_CONFIG_DIR` into user steps, mirroring how the AWS variant injects `AWS_CONFIG_FILE`.
  - Uses `mcr.microsoft.com/azure-cli:2.61.0` for the credential-setup step. `facetscloud/actions-base-image:v1.0.0` bundles `awscli` and `kubectl` but has **no `az` CLI** (verified against the published image), so `az login` would fail there.
  - 19 unit tests covering config validation (both auth modes, mutual exclusivity, required/empty fields) and script generation (no secret inlined, correct flags per mode, missing-token guard).

### Added (follow-up)
- **Secret-manager auth mode (`cloud_account_id` + `secret_manager_path`) — now the recommended path.** The action resolves the Azure credentials at run time from the control plane's secret store using the pod's own cloud identity (IRSA), so the blueprint holds only a cloud-account id. **No secret in Terraform state, none in the Task manifest, no Kubernetes Secret to create, and no per-cluster setup.** Reuses the secret layout the `cloud_account` modules already read. Generates a fetch step on the base image (which has `aws`) plus an `az login` step on the azure-cli image, since neither image has both tools; credentials are shredded after login. Verified end to end against a live control-plane cluster: authenticated and queried a real Azure MySQL Flexible Server with no Kubernetes Secret present.

### Known limitation
`use_oidc_federation` needs a control-plane change before it can be used. Verified against a live action run: the CP creates TaskRuns with a fixed `serviceAccountName` (`facets-actions-sa`) and `podTemplate: null`, so an action cannot request its own projected volume. The pod already receives a projected token (the EKS pod-identity webhook injects one for the `eks.amazonaws.com/role-arn` annotation), but its audience is `sts.amazonaws.com` rather than `api://AzureADTokenExchange`, so Entra rejects it with `AADSTS700212`. Fix is additive on the CP side — project a second token with the Azure audience, or let actions declare a service account / pod template. **Client-secret mode works today with no CP change.**

### Why
Before this change the only way to run `az` from an action was `facets_tekton_action_kubernetes` with credentials as Tekton params, forcing a human to paste a client secret on every run. The alternative — interpolating the secret into the step `script`/`env` — persists it in **both** Terraform state and the in-cluster Task manifest, because nothing in this provider is marked `Sensitive` and step `env` accepts only literal values. This resource removes that trade-off.

### Compatibility
No changes to `facets_tekton_action_aws` or `facets_tekton_action_kubernetes`. No schema changes to existing resources. All existing tests pass unchanged.

## [1.2.1] - 2026-05-14

### Fixed
- **`facets_tekton_action_kubernetes` / `facets_tekton_action_aws` lifecycle hardening** (closes #9, #10, #11)
  - **Delete is now idempotent on NotFound** — `terraform destroy` succeeds when the Task or StepAction has already been removed out-of-band (e.g. manual cluster cleanup mid-incident). NotFound is treated as already-deleted; all other error classes (Forbidden, ServerTimeout, InternalServer, etc.) still propagate.
  - **Delete orchestration is best-effort** — if Task delete fails, StepAction delete is still attempted. Both errors aggregate into diagnostics. Combined with NotFound-idempotency, destroy retries are safe end-to-end and no longer leave orphan StepActions.
  - **Read classifies errors via `apierrors.IsNotFound`** — only genuine NotFound responses trigger state removal. Transient apiserver failures (503, 403, ServerTimeout, context cancellation) now surface as diagnostics with state retained, eliminating the silent state-corruption pathway during apiserver outages.
  - **Read detects asymmetric cluster drift** — both Task and StepAction are checked. If exactly one exists, a warning surfaces explaining the asymmetric state and the recovery paths (re-apply after deleting the surviving object, or `terraform import`).
  - **Create rollback on partial failure** — if Task creation fails after StepAction creation succeeded, the StepAction is rolled back. If the rollback itself fails, the next apply self-heals: `CreateResource` adopts existing cluster objects via Get-then-Update (safe because the resource name is a deterministic hash of identity inputs).
  - **Update is Task-first** — if Task update fails, StepAction is never touched and the cluster remains in a coherent pre-Update state (zero divergence). If Task succeeds but StepAction fails, the cluster stays functional because the Task references the StepAction by its immutable `ref.name`; operator re-runs apply to retry the StepAction update only.

### Added
- **Fake-client test harness** at `internal/provider/tekton/testfake/` — a `client-go/dynamic/fake`-backed harness with reactor-based error injection and canonical Task/StepAction fixtures, enabling unit tests of CRUD lifecycle behavior against synthetic apiserver failures without a real cluster.
- 56 unit tests covering each fix as a regression guard (NotFound idempotency, error-class classification, asymmetric drift, Create rollback + AlreadyExists adopt, Task-first Update invariant, Delete orchestration).

### Customer Impact
Closes the recurring "orphan StepAction on disable/enable cycle" failure mode reported by customers running the AWS variant (MoveInSync, CommerceIQ). The Read + Create + Update + Delete fixes eliminate every known pathway by which Tekton resources accumulated as orphans in customer clusters during transient apiserver issues.

### Technical Details
No schema changes. No breaking changes. Diagnostics for partial-failure paths (asymmetric drift, Update divergence) are surfaced as warnings/errors; CI pipelines that gate purely on `terraform apply` exit code will not block on asymmetric-drift warnings — inspect plan output if you need to fail on drift.

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

## [1.2.2] - 2026-05-14

### Fixed
- **`facets_tekton_action_kubernetes` — namespace changes now force resource recreation** — Added `RequiresReplace()` plan modifier to the `namespace` attribute. Previously, changing the namespace in Terraform configuration produced `Provider produced inconsistent result after apply: .namespace: was cty.StringVal("tekton-pipelines"), but now cty.StringVal("default")` because the `Update` path overwrote the planned namespace with the prior state value. Kubernetes resources cannot be moved between namespaces, so the correct contract is destroy+recreate. Plans now display `# forces replacement` when the namespace changes. The AWS variant is unaffected (it pins namespace to `tekton-pipelines`).
