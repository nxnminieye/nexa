package main

import (
	"os"

	nexacli "github.com/nxnminieye/nexa/cli/nexactl"
)

func main() {
	os.Exit(nexacli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
