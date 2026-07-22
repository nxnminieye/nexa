package main

import (
	"context"
	"fmt"
	"os"

	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/entexec"
	"github.com/nxnminieye/nexa/generation/internal/entityload"
	"github.com/nxnminieye/nexa/provenance"
)

func main() {
	if len(os.Args) != 3 {
		panic("repository root and schema directory are required")
	}
	schemaDir, err := provenance.ParseDomainSource(os.Args[2])
	if err != nil {
		panic(err)
	}
	document, err := entityload.LoadCurrentProcess(context.Background(), entexec.Spec{
		RepositoryRoot: os.Args[1],
		SchemaDir:      schemaDir,
	})
	if err != nil {
		if typed, ok := err.(interface {
			Code() string
			Stage() string
			Reason() string
			Pointer() string
		}); ok {
			panic(fmt.Sprintf("%s/%s/%s%s", typed.Code(), typed.Stage(), typed.Reason(), typed.Pointer()))
		}
		panic(err)
	}
	canonical, err := entity.CanonicalJSON(document)
	if err != nil {
		panic(err)
	}
	fmt.Print(string(canonical))
}
