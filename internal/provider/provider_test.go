package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestProviderMetadataAndResources(t *testing.T) {
	p := New("0.1.0")
	var metadata provider.MetadataResponse
	p.Metadata(context.Background(), provider.MetadataRequest{}, &metadata)
	if metadata.TypeName != "jira" || metadata.Version != "0.1.0" {
		t.Fatalf("metadata = %#v", metadata)
	}
	resources := p.Resources(context.Background())
	if len(resources) != 1 {
		t.Fatalf("resource count = %d, want 1", len(resources))
	}
	var resourceMetadata resource.MetadataResponse
	resources[0]().Metadata(context.Background(), resource.MetadataRequest{}, &resourceMetadata)
	if resourceMetadata.TypeName != "jira_customer_organization_sync" {
		t.Fatalf("resource type = %q", resourceMetadata.TypeName)
	}
}

func TestResourceSchemaDoesNotContainCredentialsOrDirectoryUsers(t *testing.T) {
	resourceInstance := NewCustomerOrganizationSyncResource()
	var response resource.SchemaResponse
	resourceInstance.Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics = %v", response.Diagnostics)
	}
	for _, name := range []string{"jira_api_token", "entra_client_secret", "tenant_id", "group_ids", "user_source", "customers"} {
		if _, ok := response.Schema.Attributes[name]; ok {
			t.Fatalf("schema unexpectedly contains %q", name)
		}
	}
	if _, ok := response.Schema.Attributes["users_wo"]; !ok {
		t.Fatal("schema does not contain the write-only users_wo input")
	}
}

func TestValidBaseURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "https site", url: "https://example.atlassian.net", want: true},
		{name: "http site", url: "http://example.atlassian.net", want: false},
		{name: "path", url: "https://example.atlassian.net/jira", want: false},
		{name: "missing host", url: "https://", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validBaseURL(test.url); got != test.want {
				t.Fatalf("validBaseURL(%q) = %v, want %v", test.url, got, test.want)
			}
		})
	}
}
