package enthelper

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/nxnminieye/nexa/generation/internal/entexec"
	"github.com/nxnminieye/nexa/generation/internal/entipc"
	"github.com/nxnminieye/nexa/generation/internal/entityload"
)

func ExecuteV2(ctx context.Context, input []byte) ([]byte, error) {
	request, err := entipc.ParseRequestV2(entipc.HelperRequestV2Source(), input)
	if err != nil {
		return nil, err
	}
	cwd, err := filepath.EvalSymlinks(mustGetwdV2())
	if err != nil {
		return encodeV2Failure(request, entityload.InputV2Error("module_root_invalid", "/moduleDir"))
	}
	repository := cwd
	if request.ModuleDir() != "." {
		for range strings.Split(request.ModuleDir(), "/") {
			repository = filepath.Dir(repository)
		}
	}
	moduleRoot := repository
	if request.ModuleDir() != "." {
		moduleRoot = filepath.Join(repository, filepath.FromSlash(request.ModuleDir()))
	}
	if moduleRoot != cwd {
		return encodeV2Failure(request, entityload.InputV2Error("module_root_invalid", "/moduleDir"))
	}
	spec := entityload.V2Spec{RepositoryRoot: repository, ModuleDir: request.ModuleDir(), ModulePath: request.ModulePath(), SchemaDir: request.SchemaDir(), BuildTags: request.BuildTags(), Environment: os.Environ()}
	importer, err := entityload.DiscoverV2(ctx, spec)
	if err != nil {
		return encodeV2Failure(request, err)
	}
	stdout, err := entexec.RunImporterV2(ctx, moduleRoot, importer.Source, request.BuildTags(), os.Environ())
	if err != nil {
		return encodeV2Failure(request, entityload.GraphV2Error("helper_execution_failed", request.SchemaDir()))
	}
	document, err := entityload.ProjectV2(spec, importer, stdout)
	if err != nil {
		return encodeV2Failure(request, err)
	}
	result, err := entipc.NewProjectedResultV2(request, document)
	if err != nil {
		return nil, err
	}
	return entipc.CanonicalResultV2(result)
}

func encodeV2Failure(request entipc.RequestV2, err error) ([]byte, error) {
	typed, ok := err.(interface {
		Owner() string
		Code() string
		Reason() string
		Pointer() string
		Source() string
	})
	if !ok {
		return nil, err
	}
	result, encodeErr := entipc.NewDomainResultV2(typed.Owner(), typed.Code(), typed.Reason(), typed.Pointer(), typed.Source())
	if encodeErr != nil {
		return nil, encodeErr
	}
	return entipc.CanonicalResultV2(result)
}

func mustGetwdV2() string { value, _ := os.Getwd(); return value }
