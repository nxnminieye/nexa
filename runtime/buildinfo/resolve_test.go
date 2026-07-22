package buildinfo_test

import (
	"errors"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/runtime/buildinfo"
)

func TestIdentityGrammarAndAccessors(t *testing.T) {
	identity, err := buildinfo.NewIdentity("sample-api", "rpc.worker", "sample.Sample-v1")
	if err != nil {
		t.Fatalf("NewIdentity() error = %v", err)
	}
	if identity.Service() != "sample-api" || identity.Kind() != "rpc.worker" || identity.ContractVersion() != "sample.Sample-v1" {
		t.Fatalf("identity = %q/%q/%q", identity.Service(), identity.Kind(), identity.ContractVersion())
	}

	tests := []struct {
		name, service, kind, contract, reason, pointer string
	}{
		{name: "empty service", kind: "rpc", contract: "sample.Sample", reason: "identity_service_invalid", pointer: "/service"},
		{name: "uppercase service", service: "Sample", kind: "rpc", contract: "sample.Sample", reason: "identity_service_invalid", pointer: "/service"},
		{name: "service underscore", service: "sample_api", kind: "rpc", contract: "sample.Sample", reason: "identity_service_invalid", pointer: "/service"},
		{name: "service leading digit", service: "1sample", kind: "rpc", contract: "sample.Sample", reason: "identity_service_invalid", pointer: "/service"},
		{name: "service trailing separator", service: "sample-", kind: "rpc", contract: "sample.Sample", reason: "identity_service_invalid", pointer: "/service"},
		{name: "kind space", service: "sample", kind: "rpc worker", contract: "sample.Sample", reason: "identity_kind_invalid", pointer: "/serviceKind"},
		{name: "kind slash", service: "sample", kind: "rpc/worker", contract: "sample.Sample", reason: "identity_kind_invalid", pointer: "/serviceKind"},
		{name: "contract empty", service: "sample", kind: "rpc", reason: "identity_contract_version_invalid", pointer: "/contractVersion"},
		{name: "contract underscore", service: "sample", kind: "rpc", contract: "sample_Sample", reason: "identity_contract_version_invalid", pointer: "/contractVersion"},
		{name: "contract slash", service: "sample", kind: "rpc", contract: "sample/Sample", reason: "identity_contract_version_invalid", pointer: "/contractVersion"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := buildinfo.NewIdentity(test.service, test.kind, test.contract)
			buildErr := requireBuildInfoError(t, err, "build_info_invalid", test.reason)
			if buildErr.Pointer() != test.pointer {
				t.Fatalf("Pointer() = %q, want %q", buildErr.Pointer(), test.pointer)
			}
		})
	}
}

func TestIdentityAcceptsRegexMatchingValuesLongerThan256Bytes(t *testing.T) {
	longLower := strings.Repeat("a", 257)
	longContract := "A" + strings.Repeat("b", 256)
	tests := []struct{ service, kind, contract string }{
		{service: longLower, kind: "rpc", contract: "sample.Sample"},
		{service: "sample", kind: longLower, contract: "sample.Sample"},
		{service: "sample", kind: "rpc", contract: longContract},
	}
	for _, test := range tests {
		identity, err := buildinfo.NewIdentity(test.service, test.kind, test.contract)
		if err != nil {
			t.Fatalf("NewIdentity(long value) error = %v", err)
		}
		if identity.Service() != test.service || identity.Kind() != test.kind || identity.ContractVersion() != test.contract {
			t.Fatal("long identity value was normalized")
		}
	}
}

func TestResolveRejectsInvalidIdentityBeforeReader(t *testing.T) {
	calls := 0
	_, err := buildinfo.Resolve(buildinfo.Identity{}, buildinfo.ReaderFunc(func() (*debug.BuildInfo, bool) {
		calls++
		return nil, false
	}))
	requireBuildInfoError(t, err, "build_info_invalid", "identity_service_invalid")
	if calls != 0 {
		t.Fatalf("reader calls = %d, want 0", calls)
	}
}

func TestReaderResultMatrixAndExactlyOneCall(t *testing.T) {
	identity := mustIdentity(t)
	invalidIgnored := &debug.BuildInfo{GoVersion: "bad\nvalue"}
	tests := []struct {
		name       string
		info       *debug.BuildInfo
		ok         bool
		wantReason string
	}{
		{name: "nil false", info: nil, ok: false},
		{name: "non nil false", info: invalidIgnored, ok: false},
		{name: "nil true", info: nil, ok: true, wantReason: "reader_result_invalid"},
		{name: "non nil true", info: &debug.BuildInfo{}, ok: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			info, err := buildinfo.Resolve(identity, buildinfo.ReaderFunc(func() (*debug.BuildInfo, bool) {
				calls++
				return test.info, test.ok
			}))
			if calls != 1 {
				t.Fatalf("reader calls = %d, want 1", calls)
			}
			if test.wantReason != "" {
				requireBuildInfoError(t, err, "build_info_invalid", test.wantReason)
				return
			}
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			assertFallback(t, info)
		})
	}
}

func TestFallbackAndIndependentFields(t *testing.T) {
	identity := mustIdentity(t)
	tests := []struct {
		name            string
		settings        []debug.BuildSetting
		available       bool
		commit, vcsTime string
		dirty           bool
	}{
		{name: "missing revision preserves fields", settings: []debug.BuildSetting{{Key: "vcs.time", Value: "2026-07-11T09:02:03+08:00"}, {Key: "vcs.modified", Value: "false"}}, available: false, commit: "unknown", vcsTime: "2026-07-11T01:02:03Z", dirty: true},
		{name: "revision with missing modified is conservative", settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"}}, available: true, commit: "abc123", dirty: true},
		{name: "revision clean", settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"}, {Key: "vcs.modified", Value: "false"}}, available: true, commit: "abc123", dirty: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info, err := buildinfo.Resolve(identity, buildinfo.ReaderFunc(func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{GoVersion: "go1.25.0", Main: debug.Module{Path: "example.com/sample", Version: "v1.2.3"}, Settings: test.settings}, true
			}))
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if info.Available() != test.available || info.Commit() != test.commit || info.Dirty() != test.dirty || info.VCSTime() != test.vcsTime {
				t.Fatalf("state = available=%v commit=%q dirty=%v time=%q", info.Available(), info.Commit(), info.Dirty(), info.VCSTime())
			}
			if info.GoVersion() != "go1.25.0" || info.ModulePath() != "example.com/sample" || info.ModuleVersion() != "v1.2.3" {
				t.Fatalf("build fields = %q %q %q", info.GoVersion(), info.ModulePath(), info.ModuleVersion())
			}
			if info.Identity() != identity || info.Service() != identity.Service() || info.Kind() != identity.Kind() || info.ContractVersion() != identity.ContractVersion() {
				t.Fatal("identity was not preserved")
			}
		})
	}
}

func TestResolveRejectsExplicitEmptyRelevantSetting(t *testing.T) {
	for _, key := range []string{"vcs.revision", "vcs.time", "vcs.modified"} {
		t.Run(key, func(t *testing.T) {
			_, err := buildinfo.Resolve(mustIdentity(t), buildinfo.ReaderFunc(func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: key, Value: ""}}}, true
			}))
			wantReason := map[string]string{"vcs.revision": "revision_invalid", "vcs.time": "vcs_time_invalid", "vcs.modified": "vcs_modified_invalid"}[key]
			requireBuildInfoError(t, err, "build_info_invalid", wantReason)
		})
	}
}

func TestResolveRelevantSettingValidation(t *testing.T) {
	tests := []struct {
		name, reason, pointer string
		settings              []debug.BuildSetting
	}{
		{name: "duplicate same", reason: "setting_duplicate", pointer: "/settings/1/key", settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}, {Key: "vcs.revision", Value: "abc"}}},
		{name: "duplicate different before invalid value", reason: "setting_duplicate", pointer: "/settings/1/key", settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}, {Key: "vcs.modified", Value: "INVALID"}}},
		{name: "reserved revision", reason: "revision_invalid", pointer: "/settings/0/value", settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "unknown"}}},
		{name: "revision space", reason: "revision_invalid", pointer: "/settings/0/value", settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc def"}}},
		{name: "revision non ascii", reason: "revision_invalid", pointer: "/settings/0/value", settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "版本"}}},
		{name: "revision too long", reason: "revision_invalid", pointer: "/settings/0/value", settings: []debug.BuildSetting{{Key: "vcs.revision", Value: strings.Repeat("a", 257)}}},
		{name: "time invalid", reason: "vcs_time_invalid", pointer: "/settings/0/value", settings: []debug.BuildSetting{{Key: "vcs.time", Value: "2026-07-11"}}},
		{name: "modified uppercase", reason: "vcs_modified_invalid", pointer: "/settings/0/value", settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "TRUE"}}},
		{name: "modified numeric", reason: "vcs_modified_invalid", pointer: "/settings/0/value", settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "1"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolveInjected(t, &debug.BuildInfo{Settings: test.settings})
			buildErr := requireBuildInfoError(t, err, "build_info_invalid", test.reason)
			if buildErr.Pointer() != test.pointer {
				t.Fatalf("Pointer() = %q, want %q", buildErr.Pointer(), test.pointer)
			}
		})
	}
}

func TestResolveIgnoresUnrelatedDuplicateSettings(t *testing.T) {
	info, err := resolveInjected(t, &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "GOOS", Value: "linux"}, {Key: "GOOS", Value: "darwin"},
		{Key: "vcs.revision", Value: strings.Repeat("a", 256)},
	}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !info.Available() || len(info.Commit()) != 256 {
		t.Fatalf("resolved revision = available %v commit len %d", info.Available(), len(info.Commit()))
	}
}

func TestResolveNormalizesVCSTimeToUTCRFC3339Nano(t *testing.T) {
	info, err := resolveInjected(t, &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.time", Value: "2026-07-11T09:02:03.1200+08:00"}}})
	if err != nil {
		t.Fatal(err)
	}
	if info.VCSTime() != "2026-07-11T01:02:03.12Z" {
		t.Fatalf("VCSTime() = %q", info.VCSTime())
	}
}

func TestResolveProjectedTextValidationAndOrder(t *testing.T) {
	invalidUTF8 := string([]byte{0xff})
	tests := []struct {
		name, reason, pointer string
		build                 debug.BuildInfo
	}{
		{name: "go invalid UTF8", reason: "go_version_invalid", pointer: "/goVersion", build: debug.BuildInfo{GoVersion: invalidUTF8}},
		{name: "go control", reason: "go_version_invalid", pointer: "/goVersion", build: debug.BuildInfo{GoVersion: "go1.25\n"}},
		{name: "go too long", reason: "go_version_invalid", pointer: "/goVersion", build: debug.BuildInfo{GoVersion: strings.Repeat("g", 257)}},
		{name: "module path control", reason: "module_path_invalid", pointer: "/modulePath", build: debug.BuildInfo{Main: debug.Module{Path: "example.com/\x00sample"}}},
		{name: "module path CheckPath", reason: "module_path_invalid", pointer: "/modulePath", build: debug.BuildInfo{Main: debug.Module{Path: "Example.com/sample"}}},
		{name: "module path too long", reason: "module_path_invalid", pointer: "/modulePath", build: debug.BuildInfo{Main: debug.Module{Path: "example.com/" + strings.Repeat("a", 257)}}},
		{name: "module version invalid UTF8", reason: "module_version_invalid", pointer: "/moduleVersion", build: debug.BuildInfo{Main: debug.Module{Version: invalidUTF8}}},
		{name: "module version control", reason: "module_version_invalid", pointer: "/moduleVersion", build: debug.BuildInfo{Main: debug.Module{Version: "v1.2.3\n"}}},
		{name: "GoVersion fails before module", reason: "go_version_invalid", pointer: "/goVersion", build: debug.BuildInfo{GoVersion: "bad\n", Main: debug.Module{Path: "Bad/path", Version: "v1"}, Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "TRUE"}}}},
		{name: "module path fails before version", reason: "module_path_invalid", pointer: "/modulePath", build: debug.BuildInfo{Main: debug.Module{Path: "Bad/path", Version: "v1"}, Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "TRUE"}}}},
		{name: "module version fails before settings", reason: "module_version_invalid", pointer: "/moduleVersion", build: debug.BuildInfo{Main: debug.Module{Version: "v1"}, Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "TRUE"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolveInjected(t, &test.build)
			buildErr := requireBuildInfoError(t, err, "build_info_invalid", test.reason)
			if buildErr.Pointer() != test.pointer {
				t.Fatalf("Pointer() = %q, want %q", buildErr.Pointer(), test.pointer)
			}
		})
	}
}

func TestResolveModuleVersionContract(t *testing.T) {
	accepted := []string{
		"", "(devel)", "v1.2.3", "v1.2.3-alpha.1", "v1.2.3+build.5", "v1.2.3+incompatible",
		"v0.0.0-20260711010203-0123456789ab",
	}
	for _, version := range accepted {
		t.Run("accept_"+version, func(t *testing.T) {
			info, err := resolveInjected(t, &debug.BuildInfo{Main: debug.Module{Version: version}})
			if err != nil {
				t.Fatalf("Resolve(%q) error = %v", version, err)
			}
			if info.ModuleVersion() != version {
				t.Fatalf("ModuleVersion() = %q", info.ModuleVersion())
			}
		})
	}
	for _, version := range []string{"v1", "v1.2", "1.2.3", "v1.2.3-", strings.Repeat("v", 257)} {
		t.Run("reject_"+version, func(t *testing.T) {
			_, err := resolveInjected(t, &debug.BuildInfo{Main: debug.Module{Version: version}})
			requireBuildInfoError(t, err, "build_info_invalid", "module_version_invalid")
		})
	}
}

func TestResolveSettingsOrderDoesNotChangeValidInfo(t *testing.T) {
	settings := []debug.BuildSetting{
		{Key: "vcs.revision", Value: "abc123"},
		{Key: "vcs.time", Value: "2026-07-11T01:02:03Z"},
		{Key: "vcs.modified", Value: "false"},
	}
	first, err := resolveInjected(t, &debug.BuildInfo{Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	second, err := resolveInjected(t, &debug.BuildInfo{Settings: []debug.BuildSetting{settings[2], settings[0], settings[1]}})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("Info differs by settings order:\nfirst=%#v\nsecond=%#v", first, second)
	}
}

func resolveInjected(t *testing.T, build *debug.BuildInfo) (buildinfo.Info, error) {
	t.Helper()
	return buildinfo.Resolve(mustIdentity(t), buildinfo.ReaderFunc(func() (*debug.BuildInfo, bool) { return build, true }))
}

func assertFallback(t *testing.T, info buildinfo.Info) {
	t.Helper()
	if info.Available() || info.Commit() != "unknown" || !info.Dirty() || info.VCSTime() != "" || info.GoVersion() != "" || info.ModulePath() != "" || info.ModuleVersion() != "" {
		t.Fatalf("fallback = available=%v commit=%q dirty=%v time=%q go=%q path=%q version=%q", info.Available(), info.Commit(), info.Dirty(), info.VCSTime(), info.GoVersion(), info.ModulePath(), info.ModuleVersion())
	}
}

func mustIdentity(t *testing.T) buildinfo.Identity {
	t.Helper()
	identity, err := buildinfo.NewIdentity("sample", "rpc", "sample.Sample")
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func requireBuildInfoError(t *testing.T, err error, code, reason string) *buildinfo.Error {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil")
	}
	var buildErr *buildinfo.Error
	if !errors.As(err, &buildErr) {
		t.Fatalf("error type = %T, want *buildinfo.Error", err)
	}
	if buildErr.Code() != code || buildErr.Reason() != reason {
		t.Fatalf("error = code %q reason %q, want %q %q", buildErr.Code(), buildErr.Reason(), code, reason)
	}
	return buildErr
}
