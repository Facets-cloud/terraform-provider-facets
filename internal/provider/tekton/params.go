package tekton

// BuildParamSpecs renders user-declared params as Tekton ParamSpecs.
//
// `description` and `default` are emitted only when set, so a param declared
// with just a name and type renders exactly as it did before they existed. A
// param with a default becomes optional when the action is triggered; Tekton
// requires every param without one.
func BuildParamSpecs(params []ParamModel) []interface{} {
	specs := make([]interface{}, 0, len(params))
	for _, p := range params {
		spec := map[string]interface{}{
			"name": p.Name.ValueString(),
			"type": p.Type.ValueString(),
		}
		if !p.Description.IsNull() && !p.Description.IsUnknown() && p.Description.ValueString() != "" {
			spec["description"] = p.Description.ValueString()
		}
		if !p.Default.IsNull() && !p.Default.IsUnknown() {
			spec["default"] = p.Default.ValueString()
		}
		specs = append(specs, spec)
	}
	return specs
}
