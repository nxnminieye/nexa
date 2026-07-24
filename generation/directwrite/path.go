package directwrite

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var unicodeFold = cases.Fold()

type normalizedMutationSet struct {
	scopes  []OutputScope
	writes  []OutputFile
	deletes []string
}

func normalizeMutations(input MutationSet) (normalizedMutationSet, error) {
	result := normalizedMutationSet{
		scopes:  make([]OutputScope, len(input.Scopes)),
		writes:  make([]OutputFile, len(input.Writes)),
		deletes: make([]string, len(input.Deletes)),
	}
	copy(result.scopes, input.Scopes)
	copy(result.deletes, input.Deletes)
	for index, item := range input.Writes {
		result.writes[index] = OutputFile{Path: item.Path, Content: append([]byte(nil), item.Content...)}
	}
	if len(result.scopes) == 0 {
		return normalizedMutationSet{}, directError(ErrorInvalidScope, "", "at least one output scope is required", WriteReport{}, nil)
	}
	for index := range result.scopes {
		clean, err := cleanRelativePath(result.scopes[index].Path)
		if err != nil {
			return normalizedMutationSet{}, directError(ErrorInvalidScope, result.scopes[index].Path, err.Error(), WriteReport{}, err)
		}
		result.scopes[index].Path = clean
		if result.scopes[index].Mode != OutputModeReplaceTree && result.scopes[index].Mode != OutputModeFileSet {
			return normalizedMutationSet{}, directError(ErrorInvalidScope, clean, "output mode is invalid", WriteReport{}, nil)
		}
	}
	for index := range result.writes {
		clean, err := cleanRelativePath(result.writes[index].Path)
		if err != nil {
			return normalizedMutationSet{}, directError(ErrorInvalidMutation, result.writes[index].Path, err.Error(), WriteReport{}, err)
		}
		result.writes[index].Path = clean
	}
	for index := range result.deletes {
		clean, err := cleanRelativePath(result.deletes[index])
		if err != nil {
			return normalizedMutationSet{}, directError(ErrorInvalidMutation, result.deletes[index], err.Error(), WriteReport{}, err)
		}
		result.deletes[index] = clean
	}
	sort.Slice(result.scopes, func(i, j int) bool {
		if result.scopes[i].Path == result.scopes[j].Path {
			return result.scopes[i].Mode < result.scopes[j].Mode
		}
		return result.scopes[i].Path < result.scopes[j].Path
	})
	sort.Slice(result.writes, func(i, j int) bool { return result.writes[i].Path < result.writes[j].Path })
	sort.Strings(result.deletes)
	if err := validateTopology(result); err != nil {
		return normalizedMutationSet{}, err
	}
	return result, nil
}

func cleanRelativePath(value string) (string, error) {
	if !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return "", fmt.Errorf("path contains invalid text")
	}
	if value == "" || value == "." || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || filepath.IsAbs(value) {
		return "", fmt.Errorf("path must be a clean slash-separated repository-relative path")
	}
	if path.Clean(value) != value || strings.Contains(value, "//") {
		return "", fmt.Errorf("path is not clean")
	}
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("path contains an invalid component")
		}
		if foldedComponent(part) == ".git" {
			return "", fmt.Errorf(".git and its Unicode case-fold aliases are denied")
		}
	}
	return value, nil
}

func foldedComponent(value string) string {
	return unicodeFold.String(norm.NFC.String(value))
}

func foldedPath(value string) []string {
	parts := strings.Split(value, "/")
	for index := range parts {
		parts[index] = foldedComponent(parts[index])
	}
	return parts
}

func compareTopology(left, right string) (equal, related bool) {
	a, b := foldedPath(left), foldedPath(right)
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for index := 0; index < limit; index++ {
		if a[index] != b[index] {
			return false, false
		}
	}
	return len(a) == len(b), true
}

func validateTopology(set normalizedMutationSet) error {
	for index := range set.scopes {
		for other := 0; other < index; other++ {
			equal, related := compareTopology(set.scopes[other].Path, set.scopes[index].Path)
			if equal || related {
				return directError(ErrorInvalidScope, set.scopes[index].Path, "output scopes collide or overlap", WriteReport{}, nil)
			}
		}
	}
	type action struct {
		path   string
		delete bool
	}
	actions := make([]action, 0, len(set.writes)+len(set.deletes))
	for _, item := range set.writes {
		actions = append(actions, action{path: item.Path})
	}
	for _, item := range set.deletes {
		actions = append(actions, action{path: item, delete: true})
	}
	for index, item := range actions {
		scopeIndex := -1
		for candidate, scope := range set.scopes {
			equal, related := compareTopology(scope.Path, item.path)
			if related && !equal && len(foldedPath(scope.Path)) < len(foldedPath(item.path)) {
				if scopeIndex >= 0 {
					return directError(ErrorInvalidMutation, item.path, "action belongs to more than one output scope", WriteReport{}, nil)
				}
				scopeIndex = candidate
			}
		}
		if scopeIndex < 0 {
			return directError(ErrorInvalidMutation, item.path, "action is not strictly inside an output scope", WriteReport{}, nil)
		}
		if item.delete && set.scopes[scopeIndex].Mode != OutputModeFileSet {
			return directError(ErrorInvalidMutation, item.path, "explicit deletes are allowed only in file-set scopes", WriteReport{}, nil)
		}
		for other := 0; other < index; other++ {
			equal, related := compareTopology(actions[other].path, item.path)
			if equal || related {
				return directError(ErrorInvalidMutation, item.path, "action paths collide or have ancestor/descendant topology", WriteReport{}, nil)
			}
		}
	}
	return nil
}
