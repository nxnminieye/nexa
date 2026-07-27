// Package replacetree prepares one declared generated directory for direct generation.
package replacetree

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Error reports a stable path-validation or replacement failure.
type Error struct {
	reason  string
	pointer string
	cause   error
}

func (e *Error) Error() string   { return "generated scope replacement failed" }
func (e *Error) Unwrap() error   { return e.cause }
func (e *Error) Code() string    { return "generated_scope_invalid" }
func (e *Error) Stage() string   { return "replace-tree" }
func (e *Error) Reason() string  { return e.reason }
func (e *Error) Pointer() string { return e.pointer }

// Prepare validates every declared scope, then clears and recreates generatedScope.
func Prepare(repositoryRoot, generatedScope string, extensionScopes []string) (string, error) {
	repository, err := canonicalRepository(repositoryRoot)
	if err != nil {
		return "", failure("repository_invalid", "/repo-root", err)
	}
	scopes := append([]string{generatedScope}, extensionScopes...)
	cleaned := make([]string, len(scopes))
	for index, scope := range scopes {
		pointer := "/generatedScope"
		if index > 0 {
			pointer = fmt.Sprintf("/extensionScopes/%d", index-1)
		}
		cleaned[index], err = validateScope(repository, scope, pointer)
		if err != nil {
			return "", err
		}
	}
	for left := range cleaned {
		for right := left + 1; right < len(cleaned); right++ {
			if scopesOverlap(cleaned[left], cleaned[right]) {
				return "", failure("scope_overlap", "/extensionScopes", nil)
			}
		}
	}
	target := filepath.Join(repository, filepath.FromSlash(cleaned[0]))
	if info, statErr := os.Lstat(target); statErr == nil && !info.IsDir() {
		return "", failure("generated_scope_not_directory", "/generatedScope", nil)
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return "", failure("generated_scope_unreadable", "/generatedScope", statErr)
	}
	if err := os.RemoveAll(target); err != nil {
		return "", failure("generated_scope_remove_failed", "/generatedScope", err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", failure("generated_scope_create_failed", "/generatedScope", err)
	}
	return cleaned[0], nil
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

func validateScope(repository, value, pointer string) (string, error) {
	if value == "" || strings.Contains(value, `\`) || filepath.IsAbs(value) {
		return "", failure("scope_invalid", pointer, nil)
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if cleaned == "." || !filepath.IsLocal(filepath.FromSlash(cleaned)) || cleaned != value {
		return "", failure("scope_escape", pointer, nil)
	}
	for _, component := range strings.Split(cleaned, "/") {
		if strings.EqualFold(component, ".git") {
			return "", failure("git_scope_forbidden", pointer, nil)
		}
	}
	target := filepath.Join(repository, filepath.FromSlash(cleaned))
	relative, err := filepath.Rel(repository, target)
	if err != nil || relative == "." || !filepath.IsLocal(relative) {
		return "", failure("scope_escape", pointer, err)
	}
	current := repository
	for _, component := range strings.Split(cleaned, "/") {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			break
		}
		if statErr != nil {
			return "", failure("scope_unreadable", pointer, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", failure("scope_symlink", pointer, nil)
		}
	}
	return cleaned, nil
}

func scopesOverlap(left, right string) bool {
	left = strings.ToLower(left)
	right = strings.ToLower(right)
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func failure(reason, pointer string, cause error) *Error {
	return &Error{reason: reason, pointer: pointer, cause: cause}
}
