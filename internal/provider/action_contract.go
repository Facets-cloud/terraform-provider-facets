package provider

import (
	"github.com/facets-cloud/terraform-provider-facets/internal/provider/tekton"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// actionKeyAttribute and resultAttribute are shared by facets_actions and
// facets_tekton_action_kubernetes so both render the control-plane contract
// identically.
//
// result has no Default: a default would add `result = false` to the plan of
// every existing action, which is exactly the diff these attributes must not
// cause. Null is treated as false.
func actionKeyAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		Description: "Key of the action as declared in the module's facets.yaml. Written to the " +
			"`" + tekton.ActionKeyLabel + "` label, which Tekton copies onto every TaskRun and the " +
			"Facets control plane uses to identify the action. Must match `" + tekton.ActionKeyPattern +
			"` and end with a letter or digit. Changing it updates the Task in place.",
		Optional: true,
		Validators: []validator.String{
			stringvalidator.RegexMatches(tekton.ActionKeyRegexp,
				"must match "+tekton.ActionKeyPattern),
			stringvalidator.RegexMatches(tekton.ActionKeyEndRegexp,
				"must end with a letter or digit: it is used as a Kubernetes label value"),
		},
	}
}

func resultAttribute() schema.BoolAttribute {
	return schema.BoolAttribute{
		Description: "Whether the action returns a result. When true, the Task declares a `" +
			tekton.ResultName + "` result and every step receives `" + tekton.ResultFileEnvVar +
			"`, the path to write one JSON object to; the control plane validates it against the " +
			"action's result schema in facets.yaml. Requires `action_key`. Defaults to false.",
		Optional: true,
	}
}

// actionContract converts the two attributes into the shared contract. An
// unknown or null value contributes nothing.
func actionContract(actionKey types.String, result types.Bool) tekton.ActionContract {
	return tekton.ActionContract{
		ActionKey: actionKey.ValueString(),
		Result:    result.ValueBool(),
	}
}

// validateActionContract reports `result = true` without `action_key`. Unknown
// values are skipped: they are checked again once known.
func validateActionContract(actionKey types.String, result types.Bool) diag.Diagnostics {
	var diags diag.Diagnostics
	if actionKey.IsUnknown() || result.IsUnknown() {
		return diags
	}
	if err := tekton.ValidateActionContract(actionKey.ValueString(), result.ValueBool()); err != nil {
		diags.AddAttributeError(path.Root("result"), "Invalid action result configuration", err.Error())
	}
	return diags
}

// reservedEnvDiags reports a step that sets FACETS_RESULT_FILE itself. Only
// enforced when result is true, so a configuration that predates results keeps
// validating exactly as it did.
func reservedEnvDiags(stepName string, envNames []string) diag.Diagnostics {
	var diags diag.Diagnostics
	if err := tekton.CheckReservedEnv(stepName, envNames); err != nil {
		diags.AddAttributeError(path.Root("steps"), "Reserved environment variable", err.Error())
	}
	return diags
}

// syncActionContract refreshes action_key and result from the Task in the
// cluster, so an out-of-band edit to the label or results shows as drift.
//
// A result that is absent keeps a null (or false) value as it is, rather than
// writing false: configuration that never mentions `result` has null in state,
// and replacing it with false would show a diff on every plan.
func syncActionContract(task *unstructured.Unstructured, actionKey *types.String, result *types.Bool) {
	contract, hasKey := tekton.ActionContractFromTask(task)

	if hasKey {
		*actionKey = types.StringValue(contract.ActionKey)
	} else {
		*actionKey = types.StringNull()
	}

	switch {
	case contract.Result:
		*result = types.BoolValue(true)
	case result.ValueBool():
		*result = types.BoolValue(false)
	}
}
