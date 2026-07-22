package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/nxnminieye/nexa/nexactl/host"
	sourceadapter "github.com/nxnminieye/nexa/plugins/nexactl/source"
	core "github.com/nxnminieye/nexa/plugins/service/core"
	"github.com/nxnminieye/nexa/sourceplugin/engine"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

type mergeDriver struct{}

func (mergeDriver) Merge(_ context.Context, input engine.TextMergeInput) (engine.TextMergeResult, error) {
	return engine.NewTextMergeResult(input.New, true), nil
}

func main() {
	provider, err := core.New()
	if err != nil {
		panic(err)
	}
	if len(os.Args) == 2 && os.Args[1] == "ref" {
		ref, err := release.FromProvider(provider)
		if err != nil {
			panic(err)
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{
			"provider": ref.ProviderID(), "version": ref.Version(),
			"manifestDigest": ref.ManifestDigest().String(), "treeDigest": ref.TreeDigest().String(),
		})
		return
	}
	cacheLimits := release.DefaultCacheLimits()
	cacheRoot := requiredDirectory("NEXA_SOURCE_CACHE")
	cache, err := release.OpenDirectoryCache(cacheRoot, cacheLimits)
	if err != nil {
		if projected, ok := err.(*release.Error); ok {
			panic(fmt.Sprintf("open source cache: stage=%s reason=%s pointer=%s", projected.Stage(), projected.Reason(), projected.Pointer()))
		}
		panic(err)
	}
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		panic(err)
	}
	goExecutable, err = filepath.EvalSymlinks(goExecutable)
	if err != nil {
		panic(err)
	}
	adapter, err := sourceadapter.New(sourceadapter.Options{
		Version: "v0.1.0", Cache: cache, CacheLimits: cacheLimits,
		TreeLimits: cacheLimits.Tree, LockLimits: lock.DefaultLimits(), MergeDriver: mergeDriver{},
		Executor: engine.NewOSExecutor(), GoToolchain: engine.GoToolchain{
			Executable: goExecutable, Home: requiredDirectory("HOME"), TempDir: requiredDirectory("TMPDIR"),
			GOPATH: requiredDirectory("GOPATH"), ModuleCache: requiredDirectory("GOMODCACHE"), BuildCache: requiredDirectory("GOCACHE"),
		},
	}, provider)
	if err != nil {
		panic(err)
	}
	cli, err := host.New(host.Options{Name: "nexactl-core-source", Version: "v0.1.0"}, adapter)
	if err != nil {
		panic(err)
	}
	os.Exit(cli.Execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func requiredDirectory(name string) string {
	value := os.Getenv(name)
	if value == "" {
		panic(name + " is required")
	}
	return filepath.Clean(value)
}
