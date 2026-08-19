package provider

import (
	"context"
	"log/slog"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/zaphiro-technologies/terraform-provider-jira-customer-org/internal/model"
	"github.com/zaphiro-technologies/terraform-provider-jira-customer-org/internal/syncer"
)

var _ resource.Resource = (*CustomerOrganizationSyncResource)(nil)
var _ resource.ResourceWithConfigValidators = (*CustomerOrganizationSyncResource)(nil)

type CustomerOrganizationSyncResource struct {
	run    func(context.Context, syncer.Config, *slog.Logger) (syncer.Result, error)
	logger *slog.Logger
}

func NewCustomerOrganizationSyncResource() resource.Resource {
	return &CustomerOrganizationSyncResource{
		run: syncer.Run,
	}
}

type syncResourceModel struct {
	OrganizationName types.String `tfsdk:"organization_name"`
	ServiceDeskID    types.String `tfsdk:"service_desk_id"`
	BaseURL          types.String `tfsdk:"base_url"`
	Users            types.List   `tfsdk:"users_wo"`
	MembershipMode   types.String `tfsdk:"membership_mode"`
	SyncTrigger      types.String `tfsdk:"sync_trigger"`
}

type customerUserModel struct {
	Email       types.String `tfsdk:"email"`
	DisplayName types.String `tfsdk:"display_name"`
}

func (r *CustomerOrganizationSyncResource) Metadata(_ context.Context, _ resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = "jira_customer_organization_sync"
}

func (r *CustomerOrganizationSyncResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		Description: "Synchronizes externally supplied users into a Jira Service Management customer organization using additive-only semantics.",
		Attributes: map[string]schema.Attribute{
			"organization_name": schema.StringAttribute{
				Required:    true,
				Description: "Jira Service Management organization name to find or create.",
			},
			"service_desk_id": schema.StringAttribute{
				Required:    true,
				Description: "Jira Service Management service desk ID or project key.",
			},
			"base_url": schema.StringAttribute{
				Required:    true,
				Description: "Jira Cloud site URL, for example https://example.atlassian.net.",
			},
			"users_wo": schema.ListNestedAttribute{
				Required:    true,
				WriteOnly:   true,
				Description: "Normalized desired customers supplied by an external identity provider or module. Values are consumed during reconciliation and are not stored in Terraform state.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"email": schema.StringAttribute{
							Required:    true,
							WriteOnly:   true,
							Description: "Customer email address.",
						},
						"display_name": schema.StringAttribute{
							Optional:    true,
							WriteOnly:   true,
							Description: "Optional customer display name.",
						},
					},
				},
			},
			"membership_mode": schema.StringAttribute{
				Required:    true,
				Description: "Membership behavior. Only additive is supported in v1.",
			},
			"sync_trigger": schema.StringAttribute{
				Required:    true,
				Description: "Stable operator-controlled value that requests a new reconciliation when changed.",
			},
		},
	}
}

func (r *CustomerOrganizationSyncResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{syncConfigValidator{}}
}

func (r *CustomerOrganizationSyncResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var data syncResourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &data)...)
	if response.Diagnostics.HasError() {
		return
	}

	cfg, diagnostics := configFromModel(ctx, data)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}

	if _, err := r.runner()(ctx, cfg); err != nil {
		response.Diagnostics.AddError("Jira customer synchronization failed", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &data)...)
}

func (r *CustomerOrganizationSyncResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	// This is an execution resource rather than a representation of a single
	// Jira API object. Read intentionally does not query Jira or Entra: doing so
	// would turn every provider refresh into a directory sync and would not
	// provide authoritative membership semantics. The stored configuration is
	// retained as-is.
	var data syncResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &data)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &data)...)
}

func (r *CustomerOrganizationSyncResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var data syncResourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &data)...)
	if response.Diagnostics.HasError() {
		return
	}

	cfg, diagnostics := configFromModel(ctx, data)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}

	if _, err := r.runner()(ctx, cfg); err != nil {
		response.Diagnostics.AddError("Jira customer synchronization failed", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &data)...)
}

func (r *CustomerOrganizationSyncResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
	// Additive synchronization has no destructive delete behavior. Terraform
	// removes the resource from state after this method returns.
}

func (r *CustomerOrganizationSyncResource) runner() func(context.Context, syncer.Config) (syncer.Result, error) {
	logger := r.logger
	return func(ctx context.Context, cfg syncer.Config) (syncer.Result, error) {
		return r.run(ctx, cfg, logger)
	}
}

func configFromModel(ctx context.Context, data syncResourceModel) (syncer.Config, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	var users []customerUserModel
	diagnostics.Append(data.Users.ElementsAs(ctx, &users, false)...)
	if diagnostics.HasError() {
		return syncer.Config{}, diagnostics
	}
	customerUsers := make([]model.CustomerUser, 0, len(users))
	for _, user := range users {
		customerUsers = append(customerUsers, model.CustomerUser{
			Email:       user.Email.ValueString(),
			DisplayName: user.DisplayName.ValueString(),
		})
	}

	return syncer.Config{
		OrganizationName: data.OrganizationName.ValueString(),
		ServiceDeskID:    data.ServiceDeskID.ValueString(),
		BaseURL:          data.BaseURL.ValueString(),
		Users:            customerUsers,
		MembershipMode:   data.MembershipMode.ValueString(),
	}, diagnostics
}

type syncConfigValidator struct{}

func (syncConfigValidator) Description(context.Context) string {
	return "validates Jira customer synchronization configuration"
}

func (syncConfigValidator) MarkdownDescription(context.Context) string {
	return "validates Jira customer synchronization configuration"
}

func (syncConfigValidator) ValidateResource(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var data syncResourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &data)...)
	if response.Diagnostics.HasError() {
		return
	}
	if data.OrganizationName.IsNull() || (!data.OrganizationName.IsUnknown() && strings.TrimSpace(data.OrganizationName.ValueString()) == "") {
		response.Diagnostics.AddAttributeError(path.Root("organization_name"), "Invalid organization name", "organization_name must not be empty.")
	}
	if data.ServiceDeskID.IsNull() || (!data.ServiceDeskID.IsUnknown() && strings.TrimSpace(data.ServiceDeskID.ValueString()) == "") {
		response.Diagnostics.AddAttributeError(path.Root("service_desk_id"), "Invalid service desk ID", "service_desk_id must not be empty.")
	}
	if data.BaseURL.IsNull() || (!data.BaseURL.IsUnknown() && !validBaseURL(data.BaseURL.ValueString())) {
		response.Diagnostics.AddAttributeError(path.Root("base_url"), "Invalid Jira base URL", "base_url must be an HTTPS site URL without a path.")
	}
	if data.MembershipMode.IsNull() || (!data.MembershipMode.IsUnknown() && data.MembershipMode.ValueString() != "additive") {
		response.Diagnostics.AddAttributeError(path.Root("membership_mode"), "Unsupported membership mode", "Only membership_mode=additive is supported in v1.")
	}
	if data.SyncTrigger.IsNull() || (!data.SyncTrigger.IsUnknown() && strings.TrimSpace(data.SyncTrigger.ValueString()) == "") {
		response.Diagnostics.AddAttributeError(path.Root("sync_trigger"), "Invalid sync trigger", "sync_trigger must not be empty.")
	}
}

func validBaseURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == ""
}
