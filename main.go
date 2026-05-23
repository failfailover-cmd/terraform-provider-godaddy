package main

import (
	"context"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/failfailover-cmd/terraform-provider-godaddy/internal/provider"
)

func main() {
	err := providerserver.Serve(context.Background(), provider.New("dev"), providerserver.ServeOpts{
		Address: "registry.terraform.io/failfailover-cmd/godaddy",
	})
	if err != nil {
		log.Fatal(err)
	}
}
