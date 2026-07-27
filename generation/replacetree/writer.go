// Package replacetree prepares declared generated and user-logic outputs for direct generation.
package replacetree

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Error reports a stable path-validation or direct-write failure.
type Error struct {
	reason  string
	pointer string
	cause   error
}

func (e *Error) Error() string   { return "generation output write failed" }
func (e *Error) Unwrap() error   { return e.cause }
func (e *Error) Code() string    { return "generation_output_invalid" }
func (e *Error) Stage() string   { return "direct-write" }
func (e *Error) Reason() string  { return e.reason }
func (e *Error) Pointer() string { return e.pointer }

// UserLogicFile declares one exact create-once user-logic output.
type UserLogicFile struct {
	Path    string
	Content []byte
}

// UserLogicAction reports what direct generation did to one user-logic file.
type UserLogicAction string

const (
	UserLogicCreated     UserLogicAction = "created"
	UserLogicSkipped     UserLogicAction = "skipped"
	UserLogicOverwritten UserLogicAction = "overwritten"
)

// UserLogicResult reports the action for one declared user-logic file.
type UserLogicResult struct {
	Path   string
	Action UserLogicAction
}

// Prepared contains output paths validated before the generated tree was replaced.
type Prepared struct {
	repository     string
	generatedScope string
	userLogic      []UserLogicFile
}

// Prepare validates every declared output, then clears and recreates generatedScope.
func Prepare(repositoryRoot, generatedScope string, extensionScopes []string, userLogic []UserLogicFile) (Prepared, error) {
	repository, err := canonicalRepository(repositoryRoot)
	if err != nil {
		return Prepared{}, failure("repository_invalid", "/repo-root", err)
	}
	values := make([]string, 0, 1+len(extensionScopes)+len(userLogic))
	pointers := make([]string, 0, cap(values))
	values = append(values, generatedScope)
	pointers = append(pointers, "/generatedScope")
	for index, scope := range extensionScopes {
		values = append(values, scope)
		pointers = append(pointers, fmt.Sprintf("/extensionScopes/%d", index))
	}
	for index, target := range userLogic {
		values = append(values, target.Path)
		pointers = append(pointers, fmt.Sprintf("/userLogic/%d/path", index))
	}

	cleaned := make([]string, len(values))
	for index, value := range values {
		cleaned[index], err = validatePath(repository, value, pointers[index])
		if err != nil {
			return Prepared{}, err
		}
	}
	for left := range cleaned {
		for right := left + 1; right < len(cleaned); right++ {
			if pathsOverlap(cleaned[left], cleaned[right]) {
				return Prepared{}, failure("output_overlap", pointers[right], nil)
			}
		}
	}

	target := filepath.Join(repository, filepath.FromSlash(cleaned[0]))
	if info, statErr := os.Lstat(target); statErr == nil && !info.IsDir() {
		return Prepared{}, failure("generated_scope_not_directory", "/generatedScope", nil)
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return Prepared{}, failure("generated_scope_unreadable", "/generatedScope", statErr)
	}
	logicOffset := 1 + len(extensionScopes)
	preparedLogic := make([]UserLogicFile, len(userLogic))
	for index, value := range userLogic {
		path := filepath.Join(repository, filepath.FromSlash(cleaned[logicOffset+index]))
		if info, statErr := os.Lstat(path); statErr == nil && info.IsDir() {
			return Prepared{}, failure("user_logic_not_file", pointers[logicOffset+index], nil)
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return Prepared{}, failure("user_logic_unreadable", pointers[logicOffset+index], statErr)
		}
		preparedLogic[index] = UserLogicFile{Path: cleaned[logicOffset+index], Content: append([]byte(nil), value.Content...)}
	}

	if err := os.RemoveAll(target); err != nil {
		return Prepared{}, failure("generated_scope_remove_failed", "/generatedScope", err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return Prepared{}, failure("generated_scope_create_failed", "/generatedScope", err)
	}
	return Prepared{repository: repository, generatedScope: cleaned[0], userLogic: preparedLogic}, nil
}

// GeneratedScope returns the normalized generated-only directory.
func (p Prepared) GeneratedScope() string { return p.generatedScope }

// WriteUserLogic applies create-once behavior to the exact declared targets.
func (p Prepared) WriteUserLogic(overwrite bool) ([]UserLogicResult, error) {
	results := make([]UserLogicResult, 0, len(p.userLogic))
	for index, target := range p.userLogic {
		pointer := fmt.Sprintf("/userLogic/%d/path", index)
		path := filepath.Join(p.repository, filepath.FromSlash(target.Path))
		_, err := os.Lstat(path)
		switch {
		case err == nil && !overwrite:
			results = append(results, UserLogicResult{Path: target.Path, Action: UserLogicSkipped})
			continue
		case err != nil && !errors.Is(err, os.ErrNotExist):
			return nil, failure("user_logic_unreadable", pointer, err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, failure("user_logic_parent_create_failed", pointer, err)
		}
		if err == nil {
			if writeErr := os.WriteFile(path, target.Content, 0o644); writeErr != nil {
				return nil, failure("user_logic_overwrite_failed", pointer, writeErr)
			}
			results = append(results, UserLogicResult{Path: target.Path, Action: UserLogicOverwritten})
			continue
		}
		file, createErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if createErr != nil {
			return nil, failure("user_logic_create_failed", pointer, createErr)
		}
		_, writeErr := file.Write(target.Content)
		closeErr := file.Close()
		if writeErr != nil {
			return nil, failure("user_logic_create_failed", pointer, writeErr)
		}
		if closeErr != nil {
			return nil, failure("user_logic_create_failed", pointer, closeErr)
		}
		results = append(results, UserLogicResult{Path: target.Path, Action: UserLogicCreated})
	}
	return results, nil
}

func canonicalRepository(value string) (string, error) {
	if value == "" || !filepath.IsAbs(value) {
		return "", os.ErrInvalid
	}
	value, err := filepath.EvalSymlinks(filepath.Clean(value))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(value)
	if err != nil || !info.IsDir() {
		return "", os.ErrInvalid
	}
	return value, nil
}

func validatePath(repository, value, pointer string) (string, error) {
	if value == "" || strings.Contains(value, `\`) || filepath.IsAbs(value) {
		return "", failure("path_invalid", pointer, nil)
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if cleaned == "." || !filepath.IsLocal(filepath.FromSlash(cleaned)) || cleaned != value {
		return "", failure("path_escape", pointer, nil)
	}
	for _, component := range strings.Split(cleaned, "/") {
		if strings.EqualFold(component, ".git") {
			return "", failure("git_path_forbidden", pointer, nil)
		}
	}
	target := filepath.Join(repository, filepath.FromSlash(cleaned))
	relative, err := filepath.Rel(repository, target)
	if err != nil || relative == "." || !filepath.IsLocal(relative) {
		return "", failure("path_escape", pointer, err)
	}
	current := repository
	for _, component := range strings.Split(cleaned, "/") {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			break
		}
		if statErr != nil {
			return "", failure("path_unreadable", pointer, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", failure("path_symlink", pointer, nil)
		}
	}
	return cleaned, nil
}

func pathsOverlap(left, right string) bool {
	left = strings.ToLower(left)
	right = strings.ToLower(right)
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func failure(reason, pointer string, cause error) *Error {
	return &Error{reason: reason, pointer: pointer, cause: cause}
}
