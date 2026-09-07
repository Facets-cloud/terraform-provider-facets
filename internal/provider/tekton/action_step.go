package tekton

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// ActionStepModel is a step of a cloud-agnostic action.
//
// Its environment is a plain map rather than a list of name/value objects.
// Tekton's wire format is always a list, so the map is a Terraform-side
// ergonomic and is flattened here in sorted key order -- Go map iteration is
// randomised, and an unstable order shows a diff on every plan against an
// otherwise identical Task.
//
// There is no place to put a credential. Credentials reach the pod through
// envFrom on the whole step, sourced from a Secret the provider maintains from
// its own environment, so no credential passes through Terraform at all.
type ActionStepModel struct {
	Name      types.String `tfsdk:"name"`
	Image     types.String `tfsdk:"image"`
	Script    types.String `tfsdk:"script"`
	Env       types.Map    `tfsdk:"env"`
	Resources types.Object `tfsdk:"resources"`
}

// BuildActionStep renders one step. credentialsSecret, when non-empty, is
// attached as an envFrom secretRef so every key in that Secret arrives as an
// environment variable without the provider having to know any of their names --
// which is what makes one resource work for every cloud.
func BuildActionStep(ctx context.Context, step ActionStepModel, credentialsSecret string) (map[string]interface{}, error) {
	out := map[string]interface{}{
		"name":   step.Name.ValueString(),
		"image":  step.Image.ValueString(),
		"script": step.Script.ValueString(),
	}

	env, err := buildPlainEnv(ctx, step.Env)
	if err != nil {
		return nil, fmt.Errorf("step %q: %w", step.Name.ValueString(), err)
	}
	if len(env) > 0 {
		out["env"] = env
	}

	if credentialsSecret != "" {
		out["envFrom"] = []interface{}{
			map[string]interface{}{
				"secretRef": map[string]interface{}{"name": credentialsSecret},
			},
		}
	}

	if res := buildComputeResources(ctx, step.Resources); res != nil {
		out["computeResources"] = res
	}

	return out, nil
}

// buildPlainEnv flattens the non-sensitive env map into Tekton's list form.
func buildPlainEnv(ctx context.Context, env types.Map) ([]interface{}, error) {
	if env.IsNull() || env.IsUnknown() {
		return nil, nil
	}

	kv := map[string]string{}
	if diags := env.ElementsAs(ctx, &kv, false); diags.HasError() {
		return nil, fmt.Errorf("reading env: %v", diags.Errors())
	}

	names := make([]string, 0, len(kv))
	for n := range kv {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]interface{}, 0, len(names))
	for _, n := range names {
		out = append(out, map[string]interface{}{"name": n, "value": kv[n]})
	}
	return out, nil
}

// buildComputeResources converts the optional resources object, returning nil
// when nothing was set so the caller can omit the field.
func buildComputeResources(ctx context.Context, resources types.Object) map[string]interface{} {
	if resources.IsNull() || resources.IsUnknown() {
		return nil
	}
	var cr ComputeResourcesModel
	if diags := resources.As(ctx, &cr, basetypes.ObjectAsOptions{}); diags.HasError() {
		return nil
	}

	out := map[string]interface{}{}
	if !cr.Requests.IsNull() {
		m := map[string]string{}
		cr.Requests.ElementsAs(ctx, &m, false)
		if len(m) > 0 {
			out["requests"] = m
		}
	}
	if !cr.Limits.IsNull() {
		m := map[string]string{}
		cr.Limits.ElementsAs(ctx, &m, false)
		if len(m) > 0 {
			out["limits"] = m
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
