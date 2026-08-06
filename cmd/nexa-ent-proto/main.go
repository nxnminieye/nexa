package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/nxnminieye/nexa/generation/entproto"
)

func main() {
	schemaDir := flag.String("schema-dir", "", "repository-relative Ent schema directory")
	serviceID := flag.String("service", "", "service identifier")
	protoPackage := flag.String("proto-package", "", "Proto package")
	goPackage := flag.String("go-package", "", "Proto go_package option")
	multiTenant := flag.Bool("multi-tenant", false, "allow tenant-scoped entities")
	flag.Parse()
	if *schemaDir == "" || *serviceID == "" || *protoPackage == "" || *goPackage == "" {
		fmt.Fprintln(os.Stderr, "schema-dir, service, proto-package and go-package are required")
		os.Exit(2)
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	result, err := entproto.Generate(context.Background(), entproto.Options{
		RepositoryRoot: root, SchemaDir: *schemaDir, ServiceID: *serviceID,
		ProtoPackage: *protoPackage, GoPackage: *goPackage, MultiTenant: *multiTenant,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(result)
}
