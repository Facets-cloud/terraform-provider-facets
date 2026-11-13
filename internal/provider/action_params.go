package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// paramsAttribute is the `params` schema shared by every action resource.
// Each element is decoded into tekton.ParamModel, so all three resources must
// declare the same attributes.
func paramsAttribute(description string) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		Description: description,
		Optional:    true,
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"name": schema.StringAttribute{
					Description: "Parameter name",
					Required:    true,
					Validators: []validator.String{
						stringvalidator.LengthAtLeast(1),
						stringvalidator.LengthAtMost(253),
					},
				},
				"type": schema.StringAttribute{
					Description: "Parameter type: string, array or object",
					Required:    true,
					Validators: []validator.String{
						stringvalidator.OneOf("string", "array", "object"),
					},
				},
				"description": schema.StringAttribute{
					Description: "Help text shown next to the parameter when the action is triggered",
					Optional:    true,
				},
				"default": schema.StringAttribute{
					Description: "Value used when the user leaves the parameter empty. Setting it makes the " +
						"parameter optional; without it the parameter is required. Only for `string` parameters.",
					Optional: true,
				},
			},
			Validators: []validator.Object{paramDefaultValidator{}},
		},
	}
}

// paramDefaultValidator rejects `default` on array and object params: it is a
// single string, and Tekton expects a list or a map for those types.
type paramDefaultValidator struct{}

func (paramDefaultValidator) Description(context.Context) string {
	return "`default` is only allowed on params of type string"
}

func (v paramDefaultValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (paramDefaultValidator) ValidateObject(ctx context.Context, req validator.ObjectRequest, resp *validator.ObjectResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	attrs := req.ConfigValue.Attributes()
	def, ok := attrs["default"].(types.String)
	if !ok || def.IsNull() {
		return
	}
	typ, ok := attrs["type"].(types.String)
	if !ok || typ.IsNull() || typ.IsUnknown() || typ.ValueString() == "string" {
		return
	}
	resp.Diagnostics.AddAttributeError(req.Path.AtName("default"), "Invalid param default",
		fmt.Sprintf("`default` is only supported on params of type string, not %q.", typ.ValueString()))
}
