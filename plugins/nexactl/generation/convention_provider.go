package generation

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
)

const ConventionalProviderID = "repository"

// ConventionalProvider resolves generation sources from the standard consumer
// repository layout without a consumer-owned service inventory.
type ConventionalProvider struct{}

func (ConventionalProvider) Descriptor() ProviderDescriptor {
	return ProviderDescriptor{ID: ConventionalProviderID, Version: "v1.0.0"}
}

func (ConventionalProvider) Resolve(ctx context.Context, repository string) (Project, error) {
	entries, err := os.ReadDir(filepath.Join(repository, "backend"))
	if err != nil {
		return Project{}, fmt.Errorf("read conventional backend services: %w", err)
	}

	project := Project{}
	for _, entry := range entries {
		if !entry.IsDir() || !serviceIDPattern.MatchString(entry.Name()) {
			continue
		}
		service, found, err := resolveConventionalService(ctx, repository, entry.Name())
		if err != nil {
			return Project{}, err
		}
		if found {
			project.Services = append(project.Services, service)
		}
	}
	return project, nil
}

func resolveConventionalService(ctx context.Context, repository, serviceID string) (ServiceProject, bool, error) {
	service := ServiceProject{ServiceID: serviceID}
	descDirectory := filepath.Join("backend", serviceID, "desc")
	rejectedRPCDirectory := filepath.Join("backend", serviceID, "rpc", "desc")
	if serviceID == "core" {
		descDirectory = filepath.Join("backend", "core", "rpc", "desc")
		rejectedRPCDirectory = filepath.Join("backend", "core", "desc")
	}
	if drift, err := conventionalSources(filepath.Join(repository, rejectedRPCDirectory), ".proto"); err != nil {
		return ServiceProject{}, false, fmt.Errorf("read %s Proto sources: %w", serviceID, err)
	} else if len(drift) != 0 {
		return ServiceProject{}, false, fmt.Errorf("%s Proto sources use unsupported path %s", serviceID, filepath.ToSlash(rejectedRPCDirectory))
	}

	protoEntries, err := conventionalSources(filepath.Join(repository, descDirectory), ".proto")
	if err != nil {
		return ServiceProject{}, false, fmt.Errorf("read %s Proto sources: %w", serviceID, err)
	}
	if len(protoEntries) != 0 {
		resolver := conventionalDirectoryResolver(filepath.Join(repository, descDirectory))
		documents := make([]genprotocol.Document, 0, len(protoEntries))
		for _, entry := range protoEntries {
			document, compileErr := genprotocol.Compile(ctx, genprotocol.CompileOptions{
				ServiceID:  serviceID,
				EntryFiles: []string{entry},
				Resolver:   resolver,
			})
			if compileErr != nil {
				return ServiceProject{}, false, fmt.Errorf("compile %s Proto source %s: %w", serviceID, entry, compileErr)
			}
			documents = append(documents, document)
		}
		document, mergeErr := genprotocol.Merge(documents...)
		if mergeErr != nil {
			return ServiceProject{}, false, fmt.Errorf("merge %s Proto sources: %w", serviceID, mergeErr)
		}
		service.RPC = &RPCProject{Facts: document}
	}

	apiDirectory := descDirectory
	rejectedAPIDirectory := filepath.Join("backend", serviceID, "api", "desc")
	if serviceID == "core" {
		apiDirectory = filepath.Join("backend", "core", "api", "desc")
		rejectedAPIDirectory = filepath.Join("backend", "core", "desc")
	}
	if drift, err := conventionalSources(filepath.Join(repository, rejectedAPIDirectory), ".api"); err != nil {
		return ServiceProject{}, false, fmt.Errorf("read %s API sources: %w", serviceID, err)
	} else if len(drift) != 0 {
		return ServiceProject{}, false, fmt.Errorf("%s API sources use unsupported path %s", serviceID, filepath.ToSlash(rejectedAPIDirectory))
	}
	apiEntries, err := conventionalSources(filepath.Join(repository, apiDirectory), ".api")
	if err != nil {
		return ServiceProject{}, false, fmt.Errorf("read %s API sources: %w", serviceID, err)
	}
	if len(apiEntries) != 0 {
		if !containsString(apiEntries, "base.api") {
			return ServiceProject{}, false, fmt.Errorf("%s API sources are missing conventional entry base.api", serviceID)
		}
		service.API = &APIProject{EntryFile: filepath.ToSlash(filepath.Join(apiDirectory, "base.api"))}
	}

	return service, service.RPC != nil || service.API != nil, nil
}

func conventionalSources(directory, extension string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type().IsRegular() && filepath.Ext(entry.Name()) == extension {
			result = append(result, entry.Name())
		}
	}
	sort.Strings(result)
	return result, nil
}

func containsString(values []string, target string) bool {
	index := sort.SearchStrings(values, target)
	return index < len(values) && values[index] == target
}

type conventionalDirectoryResolver string

func (resolver conventionalDirectoryResolver) Open(_ context.Context, path string) (io.ReadCloser, error) {
	return os.Open(filepath.Join(string(resolver), filepath.FromSlash(path)))
}
