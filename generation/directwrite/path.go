package directwrite

import (
	"context"
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
	scopes          []OutputScope
	writes          []OutputFile
	deletes         []string
	scopePaths      []canonicalPath
	writeScopeModes []OutputMode
	scopeModeByKey  map[string]OutputMode
}

type canonicalPath struct{ components []string }
type pathTrieNode struct {
	children map[string]*pathTrieNode
	terminal bool
}

func newPathTrie() *pathTrieNode { return &pathTrieNode{children: map[string]*pathTrieNode{}} }

func (n *pathTrieNode) insert(p canonicalPath) (equal, related bool) {
	cur := n
	for i, part := range p.components {
		if cur.terminal {
			return false, true
		}
		next := cur.children[part]
		if next == nil {
			next = newPathTrie()
			cur.children[part] = next
		}
		cur = next
		if i == len(p.components)-1 {
			equal = cur.terminal
			related = equal || len(cur.children) > 0
			cur.terminal = true
		}
	}
	return
}

func normalizeMutations(input MutationSet) (normalizedMutationSet, error) {
	return normalizeMutationsContext(context.Background(), input)
}

func normalizeMutationsContext(ctx context.Context, input MutationSet) (normalizedMutationSet, error) {
	if err := contextErr(ctx); err != nil {
		return normalizedMutationSet{}, err
	}
	result := normalizedMutationSet{
		scopes:  make([]OutputScope, len(input.Scopes)),
		writes:  make([]OutputFile, len(input.Writes)),
		deletes: make([]string, len(input.Deletes)),
	}
	for index, item := range input.Scopes {
		if err := contextErr(ctx); err != nil {
			return normalizedMutationSet{}, err
		}
		result.scopes[index] = item
	}
	for index, item := range input.Writes {
		if err := contextErr(ctx); err != nil {
			return normalizedMutationSet{}, err
		}
		result.writes[index] = OutputFile{Path: item.Path, Content: append([]byte(nil), item.Content...)}
	}
	for index, item := range input.Deletes {
		if err := contextErr(ctx); err != nil {
			return normalizedMutationSet{}, err
		}
		result.deletes[index] = item
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
		if err := contextErr(ctx); err != nil {
			return normalizedMutationSet{}, err
		}
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
		if err := contextErr(ctx); err != nil {
			return normalizedMutationSet{}, err
		}
	}
	for index := range result.deletes {
		clean, err := cleanRelativePath(result.deletes[index])
		if err != nil {
			return normalizedMutationSet{}, directError(ErrorInvalidMutation, result.deletes[index], err.Error(), WriteReport{}, err)
		}
		result.deletes[index] = clean
		if err := contextErr(ctx); err != nil {
			return normalizedMutationSet{}, err
		}
	}
	sort.Slice(result.scopes, func(i, j int) bool {
		if result.scopes[i].Path == result.scopes[j].Path {
			return result.scopes[i].Mode < result.scopes[j].Mode
		}
		return result.scopes[i].Path < result.scopes[j].Path
	})
	sort.Slice(result.writes, func(i, j int) bool { return result.writes[i].Path < result.writes[j].Path })
	sort.Strings(result.deletes)
	result.scopePaths = make([]canonicalPath, len(result.scopes))
	for i := range result.scopes {
		p, err := canonicalizePath(ctx, result.scopes[i].Path)
		if err != nil {
			return normalizedMutationSet{}, err
		}
		result.scopePaths[i] = p
	}
	if err := validateTopology(ctx, &result); err != nil {
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

func canonicalizePath(ctx context.Context, value string) (canonicalPath, error) {
	parts := strings.Split(value, "/")
	out := canonicalPath{components: make([]string, len(parts))}
	for i, part := range parts {
		if err := contextErr(ctx); err != nil {
			return canonicalPath{}, err
		}
		out.components[i] = foldedComponent(part)
	}
	return out, nil
}

func contextErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return directErrorWithEvidence(ErrorCanceled, "", "direct write was canceled", WriteReport{}, err, ChangeEvidenceComplete)
	}
	return nil
}

func validateTopology(ctx context.Context, set *normalizedMutationSet) error {
	trie := newPathTrie()
	set.scopeModeByKey = make(map[string]OutputMode, len(set.scopes))
	for index := range set.scopes {
		if err := contextErr(ctx); err != nil {
			return err
		}
		equal, related := trie.insert(set.scopePaths[index])
		if equal || related {
			return directError(ErrorInvalidScope, set.scopes[index].Path, "output scopes collide or overlap", WriteReport{}, nil)
		}
		set.scopeModeByKey[strings.Join(set.scopePaths[index].components, "/")] = set.scopes[index].Mode
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
	actionTrie := newPathTrie()
	set.writeScopeModes = make([]OutputMode, len(set.writes))
	for index, item := range actions {
		if err := contextErr(ctx); err != nil {
			return err
		}
		p, err := canonicalizePath(ctx, item.path)
		if err != nil {
			return err
		}
		scopeMode, scopeFound := OutputMode(""), false
		for end := len(p.components) - 1; end > 0; end-- {
			if mode, ok := set.scopeModeByKey[strings.Join(p.components[:end], "/")]; ok {
				scopeMode, scopeFound = mode, true
				break
			}
		}
		if !scopeFound {
			return directError(ErrorInvalidMutation, item.path, "action is not strictly inside an output scope", WriteReport{}, nil)
		}
		if item.delete && scopeMode != OutputModeFileSet {
			return directError(ErrorInvalidMutation, item.path, "explicit deletes are allowed only in file-set scopes", WriteReport{}, nil)
		}
		equal, related := actionTrie.insert(p)
		if equal || related {
			return directError(ErrorInvalidMutation, item.path, "action paths collide or have ancestor/descendant topology", WriteReport{}, nil)
		}
		if index < len(set.writes) {
			set.writeScopeModes[index] = scopeMode
		}
	}
	return nil
}
