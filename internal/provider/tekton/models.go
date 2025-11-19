package tekton

import (
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// FacetsEnvironmentModel represents the Facets environment configuration
type FacetsEnvironmentModel struct {
	UniqueName types.String `tfsdk:"unique_name"`
}

// FacetsResourceModel represents the Facets resource configuration
type FacetsResourceModel struct {
	Kind types.String `tfsdk:"kind"`
}

// ParamModel represents a Tekton Task parameter
type ParamModel struct {
	Name types.String `tfsdk:"name"`
	Type types.String `tfsdk:"type"`
}

// StepModel represents a Tekton Task step
type StepModel struct {
	Name      types.String `tfsdk:"name"`
	Image     types.String `tfsdk:"image"`
	Script    types.String `tfsdk:"script"`
	Resources types.Object `tfsdk:"resources"`
	Env       types.List   `tfsdk:"env"`
}

// ComputeResourcesModel represents compute resources for a step
type ComputeResourcesModel struct {
	Requests types.Map `tfsdk:"requests"`
	Limits   types.Map `tfsdk:"limits"`
}

// EnvVarModel represents an environment variable
type EnvVarModel struct {
	Name  types.String `tfsdk:"name"`
	Value types.String `tfsdk:"value"`
}
