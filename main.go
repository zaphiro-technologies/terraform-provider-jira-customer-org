package main

import (
	"context"
	"log"

	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	jiraprovider "github.com/zaphiro-technologies/terraform-provider-jira-customer-org/internal/provider"
)

var version = "0.1.0"

func main() {
	err := providerserver.Serve(context.Background(), func() frameworkprovider.Provider {
		return jiraprovider.New(version)
	}, providerserver.ServeOpts{
		Address:         "registry.terraform.io/zaphiro-technologies/jira-customer-org",
		ProtocolVersion: 6,
	})
	if err != nil {
		log.Fatal(err)
	}
}
