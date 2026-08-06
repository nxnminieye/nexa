package main

import (
	"os"

	nexacli "github.com/nxnminieye/nexa/cli/nexactl"
)

var buildVersion = "v0.0.0-dev"

func main() {
	os.Exit(nexacli.RunWithOptions(
		os.Args[1:], os.Stdout, os.Stderr,
		nexacli.Options{BuildVersion: buildVersion},
	))
}
