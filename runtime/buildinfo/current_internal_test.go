package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestCurrentUsesPackageReadBuildInfoSeam(t *testing.T) {
	read := func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			GoVersion: "go1.25.0",
			Main:      debug.Module{Path: "example.com/sample", Version: "v1.2.3"},
			Settings:  []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"}},
		}, true
	}
	identity, err := NewIdentity("sample", "rpc", "sample.Sample")
	if err != nil {
		t.Fatal(err)
	}
	info, err := resolveCurrent(identity, read)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Available() || info.Commit() != "abc123" {
		t.Fatalf("Current() = available %v commit %q", info.Available(), info.Commit())
	}
}
