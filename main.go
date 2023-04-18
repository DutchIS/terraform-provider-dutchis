package main

import (
	"github.com/dutchis/terraform/dutchis"
	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
)

func main() {
	plugin.Serve(&plugin.ServeOpts{
		ProviderFunc: dutchis.Provider})
}
