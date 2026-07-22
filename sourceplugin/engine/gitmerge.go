package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

type gitMergeDriver struct {
	executable string
	tempRoot   string
}

func NewGitMergeDriver(absoluteGit, tempRoot string) (MergeDriver, error) {
	if !filepath.IsAbs(absoluteGit) {
		return nil, newError(ErrInput, "source_merge_invalid", "path_invalid", "/absoluteGit", "configure")
	}
	info, err := os.Lstat(absoluteGit)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return nil, newError(ErrInput, "source_merge_invalid", "path_invalid", "/absoluteGit", "configure")
	}
	if !filepath.IsAbs(tempRoot) {
		return nil, newError(ErrInput, "source_merge_invalid", "path_invalid", "/tempRoot", "configure")
	}
	tempInfo, err := os.Lstat(tempRoot)
	if err != nil || !tempInfo.IsDir() {
		return nil, newError(ErrInput, "source_merge_invalid", "path_invalid", "/tempRoot", "configure")
	}
	return &gitMergeDriver{executable: filepath.Clean(absoluteGit), tempRoot: filepath.Clean(tempRoot)}, nil
}

func (d *gitMergeDriver) Merge(ctx context.Context, input TextMergeInput) (result TextMergeResult, resultErr error) {
	if ctx == nil {
		return TextMergeResult{}, newError(ErrInput, "source_merge_invalid", "context_invalid", "/context", "merge")
	}
	if ctx.Err() != nil {
		return TextMergeResult{}, newError(ErrCanceled, "operation_canceled", "context_canceled", "/context", "merge")
	}
	tempDirectory, err := os.MkdirTemp(d.tempRoot, ".nexa-source-merge-")
	if err != nil {
		return TextMergeResult{}, newError(ErrExternal, "source_merge_failed", "temp_create_failed", "", "merge")
	}
	defer func() {
		if err := os.RemoveAll(tempDirectory); err != nil && resultErr == nil {
			result = TextMergeResult{}
			resultErr = newError(ErrInternal, "source_merge_failed", "temp_cleanup_failed", "", "merge")
		}
	}()

	localPath := filepath.Join(tempDirectory, "local")
	oldPath := filepath.Join(tempDirectory, "old")
	newPath := filepath.Join(tempDirectory, "new")
	for _, file := range []struct {
		path    string
		content []byte
	}{
		{path: localPath, content: input.Local},
		{path: oldPath, content: input.Old},
		{path: newPath, content: input.New},
	} {
		if err := writeMergeInput(file.path, file.content); err != nil {
			return TextMergeResult{}, newError(ErrExternal, "source_merge_failed", "temp_write_failed", "", "merge")
		}
	}

	var stdout bytes.Buffer
	command := exec.CommandContext(ctx, d.executable, "merge-file", "--stdout", "--diff3", localPath, oldPath, newPath)
	command.Stdout = &stdout
	command.Stderr = io.Discard
	err = command.Run()
	if err == nil {
		return NewTextMergeResult(stdout.Bytes(), true), nil
	}
	if ctx.Err() != nil {
		return TextMergeResult{}, newError(ErrCanceled, "operation_canceled", "context_canceled", "/context", "merge")
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
		return TextMergeResult{}, newError(ErrExternal, "source_merge_failed", "tool_failed", "", "merge")
	}
	return NewTextMergeResult(stdout.Bytes(), false), nil
}

func writeMergeInput(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
