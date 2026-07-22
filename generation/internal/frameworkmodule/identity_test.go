package frameworkmodule

import (
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"

	"github.com/nxnminieye/nexa/generation/internal/buildinput"
)

const frameworkPath = "github.com/nxnminieye/nexa"

func TestIdentityRetainsClosedReplacementVariants(t *testing.T) {
	local := canonicalTestDirectory(t, filepath.Join(t.TempDir(), "framework"))
	tests := []struct {
		name      string
		spec      IdentitySpec
		kind      ReplacementKind
		path      string
		version   string
		localPath string
	}{
		{
			name: "none",
			spec: IdentitySpec{Module: buildinput.ModuleRequirement{Path: frameworkPath, Version: "v0.8.1"}, ReplacementKind: ReplacementNone},
			kind: ReplacementNone,
		},
		{
			name: "version",
			spec: IdentitySpec{
				Module:          buildinput.ModuleRequirement{Path: frameworkPath, Version: "v0.8.1"},
				ReplacementKind: ReplacementVersion,
				ReplacementPath: "example.com/forks/nexa", ReplacementVersion: "v0.8.2",
			},
			kind: ReplacementVersion, path: "example.com/forks/nexa", version: "v0.8.2",
		},
		{
			name: "local",
			spec: IdentitySpec{
				Module:          buildinput.ModuleRequirement{Path: frameworkPath, Version: "v0.8.1"},
				ReplacementKind: ReplacementLocal, LocalPath: local,
			},
			kind: ReplacementLocal, localPath: local,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity, err := NewIdentity(test.spec)
			if err != nil {
				t.Fatal(err)
			}
			module, err := identity.Module()
			if err != nil || module != test.spec.Module {
				t.Fatalf("Module() = %#v, %v", module, err)
			}
			kind, path, version, localPath, err := identity.Replacement()
			if err != nil {
				t.Fatal(err)
			}
			if kind != test.kind || path != test.path || version != test.version || localPath != test.localPath {
				t.Fatalf("Replacement() = %q, %q, %q, %q", kind, path, version, localPath)
			}
		})
	}
}

func TestSelectFindsExactFrameworkModule(t *testing.T) {
	local := canonicalTestDirectory(t, filepath.Join(t.TempDir(), "framework"))
	tests := []struct {
		name string
		info *debug.BuildInfo
		kind ReplacementKind
	}{
		{
			name: "main module",
			info: &debug.BuildInfo{Main: debug.Module{Path: frameworkPath, Version: "v0.9.0"}},
			kind: ReplacementNone,
		},
		{
			name: "version replacement dependency",
			info: &debug.BuildInfo{Main: debug.Module{Path: "example.com/consumer", Version: "v0.0.0"}, Deps: []*debug.Module{{
				Path: frameworkPath, Version: "v0.9.0", Replace: &debug.Module{Path: "example.com/forks/nexa", Version: "v0.9.1"},
			}}},
			kind: ReplacementVersion,
		},
		{
			name: "local replacement dependency",
			info: &debug.BuildInfo{Main: debug.Module{Path: "example.com/consumer", Version: "v0.0.0"}, Deps: []*debug.Module{{
				Path: frameworkPath, Version: "v0.9.0", Replace: &debug.Module{Path: local, Version: "(devel)"},
			}}},
			kind: ReplacementLocal,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity, err := Select(test.info)
			if err != nil {
				t.Fatal(err)
			}
			kind, _, _, _, err := identity.Replacement()
			if err != nil || kind != test.kind {
				t.Fatalf("Replacement kind = %q, %v", kind, err)
			}
		})
	}
}

func TestSelectRejectsUnavailableAmbiguousAndDevelopmentIdentity(t *testing.T) {
	tests := []struct {
		name, reason string
		info         *debug.BuildInfo
	}{
		{name: "unavailable", reason: "build_info_unavailable"},
		{name: "missing", reason: "framework_module_missing", info: &debug.BuildInfo{Main: debug.Module{Path: "example.com/consumer", Version: "v0.0.0"}}},
		{name: "ambiguous", reason: "framework_module_ambiguous", info: &debug.BuildInfo{
			Main: debug.Module{Path: "example.com/consumer", Version: "v0.0.0"},
			Deps: []*debug.Module{{Path: frameworkPath, Version: "v0.9.0"}, {Path: frameworkPath, Version: "v0.9.1"}},
		}},
		{name: "development", reason: "framework_module_devel", info: &debug.BuildInfo{Main: debug.Module{Path: frameworkPath, Version: "(devel)"}}},
		{name: "blank version", reason: "framework_module_version_invalid", info: &debug.BuildInfo{Main: debug.Module{Path: frameworkPath}}},
		{name: "noncanonical version", reason: "framework_module_version_invalid", info: &debug.BuildInfo{Main: debug.Module{Path: frameworkPath, Version: "v0.9"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Select(test.info)
			var identityErr *Error
			if !errors.As(err, &identityErr) || identityErr.Code() != "framework_identity_invalid" || identityErr.Stage() != "framework-identity" || identityErr.Reason() != test.reason {
				t.Fatalf("Select() error = %#v", err)
			}
		})
	}
}

func canonicalTestDirectory(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
