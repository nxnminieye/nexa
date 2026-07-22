package engine_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nxnminieye/nexa/sourceplugin/engine"
)

const gitMergeHelperMode = "NEXA_GIT_MERGE_HELPER_MODE"

func TestMain(m *testing.M) {
	if mode := os.Getenv(gitMergeHelperMode); mode != "" {
		os.Exit(runGitMergeHelper(mode))
	}
	os.Exit(m.Run())
}

func TestNewGitMergeDriverValidatesExecutableAndTempRoot(t *testing.T) {
	executable := helperExecutable(t)
	tempRoot := t.TempDir()

	driver, err := engine.NewGitMergeDriver(executable, tempRoot)
	if err != nil || driver == nil {
		t.Fatalf("valid driver = %#v, err=%v", driver, err)
	}

	notExecutable := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(notExecutable, []byte("not executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	notDirectory := filepath.Join(t.TempDir(), "temp-root")
	if err := os.WriteFile(notDirectory, []byte("regular file"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkRoot := filepath.Join(t.TempDir(), "temp-root-link")
	if err := os.Symlink(tempRoot, symlinkRoot); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		git      string
		tempRoot string
		pointer  string
	}{
		{name: "relative git", git: "git", tempRoot: tempRoot, pointer: "/absoluteGit"},
		{name: "git directory", git: t.TempDir(), tempRoot: tempRoot, pointer: "/absoluteGit"},
		{name: "git not executable", git: notExecutable, tempRoot: tempRoot, pointer: "/absoluteGit"},
		{name: "relative temp root", git: executable, tempRoot: "tmp", pointer: "/tempRoot"},
		{name: "missing temp root", git: executable, tempRoot: filepath.Join(t.TempDir(), "missing"), pointer: "/tempRoot"},
		{name: "temp root file", git: executable, tempRoot: notDirectory, pointer: "/tempRoot"},
		{name: "temp root symlink", git: executable, tempRoot: symlinkRoot, pointer: "/tempRoot"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := engine.NewGitMergeDriver(test.git, test.tempRoot)
			assertGitMergeError(t, err, engine.ErrInput, "source_merge_invalid", "path_invalid", test.pointer, "configure")
		})
	}
}

func TestGitMergeDriverProjectsCleanAndConflictResults(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		want      string
		wantClean bool
	}{
		{name: "clean", mode: "clean", want: "merged\n", wantClean: true},
		{name: "conflict", mode: "conflict", want: "conflict-output\n", wantClean: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(gitMergeHelperMode, test.mode)
			tempRoot := t.TempDir()
			driver, err := engine.NewGitMergeDriver(helperExecutable(t), tempRoot)
			if err != nil {
				t.Fatal(err)
			}

			result, err := driver.Merge(context.Background(), engine.TextMergeInput{
				Local: []byte("local\n"), Old: []byte("old\n"), New: []byte("new\n"),
			})
			if err != nil || string(result.Bytes()) != test.want || result.Clean() != test.wantClean {
				t.Fatalf("result=%q clean=%v err=%v", result.Bytes(), result.Clean(), err)
			}
			returned := result.Bytes()
			returned[0] = 'X'
			if string(result.Bytes()) != test.want {
				t.Fatal("merge result bytes were mutable")
			}
			assertDirectoryEmpty(t, tempRoot)
		})
	}
}

func TestGitMergeDriverProjectsSafeFailureAndCancellation(t *testing.T) {
	t.Run("unexpected exit", func(t *testing.T) {
		t.Setenv(gitMergeHelperMode, "failure")
		tempRoot := t.TempDir()
		driver, err := engine.NewGitMergeDriver(helperExecutable(t), tempRoot)
		if err != nil {
			t.Fatal(err)
		}

		_, err = driver.Merge(context.Background(), engine.TextMergeInput{
			Local: []byte("local-secret\n"), Old: []byte("old-secret\n"), New: []byte("new-secret\n"),
		})
		assertGitMergeError(t, err, engine.ErrExternal, "source_merge_failed", "tool_failed", "", "merge")
		for _, secret := range []string{"local-secret", "old-secret", "new-secret", "private-stderr", "private-stdout"} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("unsafe subprocess data escaped: %q", err)
			}
		}
		assertDirectoryEmpty(t, tempRoot)
	})

	t.Run("canceled", func(t *testing.T) {
		t.Setenv(gitMergeHelperMode, "block")
		tempRoot := t.TempDir()
		driver, err := engine.NewGitMergeDriver(helperExecutable(t), tempRoot)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancelTimer := time.AfterFunc(20*time.Millisecond, cancel)
		defer cancelTimer.Stop()

		_, err = driver.Merge(ctx, engine.TextMergeInput{Local: []byte("local\n"), Old: []byte("old\n"), New: []byte("new\n")})
		assertGitMergeError(t, err, engine.ErrCanceled, "operation_canceled", "context_canceled", "/context", "merge")
		assertDirectoryEmpty(t, tempRoot)
	})

	t.Run("already canceled avoids temporary writes", func(t *testing.T) {
		tempRoot := t.TempDir()
		driver, err := engine.NewGitMergeDriver(helperExecutable(t), tempRoot)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(tempRoot); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err = driver.Merge(ctx, engine.TextMergeInput{Local: []byte("local\n"), Old: []byte("old\n"), New: []byte("new\n")})
		assertGitMergeError(t, err, engine.ErrCanceled, "operation_canceled", "context_canceled", "/context", "merge")
		if _, statErr := os.Lstat(tempRoot); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("already-canceled merge recreated temp root: %v", statErr)
		}
	})
}

func TestGitMergeDriverUsesDiff3Semantics(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is unavailable")
	}
	git, err = filepath.Abs(git)
	if err != nil {
		t.Fatal(err)
	}
	git, err = filepath.EvalSymlinks(git)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := engine.NewGitMergeDriver(git, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	clean, err := driver.Merge(context.Background(), engine.TextMergeInput{
		Old: []byte("one\ntwo\nthree\n"), Local: []byte("local\ntwo\nthree\n"), New: []byte("one\ntwo\nnew\n"),
	})
	if err != nil || !clean.Clean() || string(clean.Bytes()) != "local\ntwo\nnew\n" {
		t.Fatalf("clean diff3 = %q clean=%v err=%v", clean.Bytes(), clean.Clean(), err)
	}

	conflict, err := driver.Merge(context.Background(), engine.TextMergeInput{
		Old: []byte("base\n"), Local: []byte("local\n"), New: []byte("new\n"),
	})
	if err != nil || conflict.Clean() || len(conflict.Bytes()) == 0 {
		t.Fatalf("conflict diff3 = %q clean=%v err=%v", conflict.Bytes(), conflict.Clean(), err)
	}
}

func runGitMergeHelper(mode string) int {
	if len(os.Args) != 7 || os.Args[1] != "merge-file" || os.Args[2] != "--stdout" || os.Args[3] != "--diff3" {
		fmt.Fprint(os.Stderr, "invalid argv")
		return 2
	}
	for _, path := range os.Args[4:] {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			fmt.Fprint(os.Stderr, "invalid temp file")
			return 2
		}
	}
	switch mode {
	case "clean", "conflict", "block":
		local, localErr := os.ReadFile(os.Args[4])
		old, oldErr := os.ReadFile(os.Args[5])
		newContent, newErr := os.ReadFile(os.Args[6])
		if localErr != nil || oldErr != nil || newErr != nil || string(local) != "local\n" || string(old) != "old\n" || string(newContent) != "new\n" {
			fmt.Fprint(os.Stderr, "invalid temp content")
			return 2
		}
	}
	switch mode {
	case "clean":
		fmt.Fprint(os.Stdout, "merged\n")
		return 0
	case "conflict":
		fmt.Fprint(os.Stdout, "conflict-output\n")
		fmt.Fprint(os.Stderr, "private-stderr")
		return 1
	case "failure":
		fmt.Fprint(os.Stdout, "private-stdout")
		fmt.Fprint(os.Stderr, "private-stderr")
		return 2
	case "block":
		time.Sleep(time.Minute)
		return 0
	default:
		return 2
	}
}

func helperExecutable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func assertGitMergeError(t *testing.T, err error, class engine.ErrorClass, code, reason, pointer, stage string) {
	t.Helper()
	var projected *engine.Error
	if !errors.As(err, &projected) {
		t.Fatalf("error = %#v, want *engine.Error", err)
	}
	if projected.Class() != class || projected.Code() != code || projected.Reason() != reason || projected.Pointer() != pointer || projected.Stage() != stage {
		t.Fatalf("error = class=%v code=%q reason=%q pointer=%q stage=%q", projected.Class(), projected.Code(), projected.Reason(), projected.Pointer(), projected.Stage())
	}
}

func assertDirectoryEmpty(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary files were not removed: %#v", entries)
	}
}
