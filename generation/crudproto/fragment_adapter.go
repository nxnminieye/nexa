package crudproto

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/nxnminieye/nexa/generation/directwrite"
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/crudbuild"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	generatedFragmentMarker     = crudbuild.GeneratedProtoMarker
	maxSchemaKeyBytes           = crudbuild.MaxSchemaKeyBytes
	maxFragmentDirectoryEntries = 4096
	fragmentDirectoryBatchSize  = 128
)

type SchemaID = crudbuild.SchemaID
type SchemaKey = crudbuild.SchemaKey

type FragmentProjection struct{ state crudbuild.FragmentProjection }
type ProtoFragment struct{ state crudbuild.ProtoFragment }

type FragmentMutationSpec struct {
	RepositoryRoot string
	ProtoEntry     string
	OutputScopes   []directwrite.OutputScope
}

type guardedFragment struct {
	path    string
	present bool
	info    os.FileInfo
}

type fragmentExecutionHooks struct {
	afterScan        func() error
	onScanEntry      func(string)
	beforeMarkerRead func(string)
	onGuardRecheck   func(string)
}

var fragmentBasenamePattern = regexp.MustCompile(`^ent\.([a-z0-9]+(?:-[a-z0-9]+)*)\.generated\.proto$`)
var errFragmentGuardPlatformUnsupported = errors.New("CRUD fragment guard platform unsupported")

func BuildFragmentProjection(entities entity.Document, options BuildOptions) (FragmentProjection, error) {
	var existing *crudbuild.Lock
	if options.ExistingLock != nil {
		value := options.ExistingLock.state
		existing = &value
	}
	projection, err := crudbuild.BuildFragments(entities, crudbuild.FragmentSpec{
		ServiceID:    options.ServiceID,
		ProtoPackage: options.ProtoPackage,
		GoPackage:    options.GoPackage,
		ExistingLock: existing,
		MultiTenant:  crudbuild.MultiTenantConfig{Enabled: options.MultiTenant.Enabled},
	})
	if err != nil {
		return FragmentProjection{}, wrapError(err)
	}
	return FragmentProjection{state: projection}, nil
}

func WriteFragmentProjection(ctx context.Context, spec FragmentMutationSpec, projection FragmentProjection) (directwrite.WriteReport, error) {
	return writeFragmentProjection(ctx, spec, projection, fragmentExecutionHooks{})
}

func writeFragmentProjection(ctx context.Context, spec FragmentMutationSpec, projection FragmentProjection, hooks fragmentExecutionHooks) (directwrite.WriteReport, error) {
	if ctx == nil {
		return directwrite.WriteReport{}, newHostError("validate-input", "context_invalid", "/context", "")
	}
	mutations, guards, err := buildFragmentExecution(ctx, spec, projection, hooks)
	if err != nil {
		return directwrite.WriteReport{}, err
	}
	if hooks.afterScan != nil {
		if err := hooks.afterScan(); err != nil {
			return directwrite.WriteReport{}, err
		}
	}
	if err := recheckGuardedFragments(ctx, spec, guards, hooks); err != nil {
		return directwrite.WriteReport{}, err
	}
	return directwrite.Write(ctx, spec.RepositoryRoot, mutations)
}

func buildFragmentExecution(ctx context.Context, spec FragmentMutationSpec, projection FragmentProjection, hooks fragmentExecutionHooks) (directwrite.MutationSet, []guardedFragment, error) {
	if err := contextError(ctx); err != nil {
		return directwrite.MutationSet{}, nil, err
	}
	if !projection.state.Valid() {
		return directwrite.MutationSet{}, nil, newHostError("validate-input", "fragment_projection_invalid", "/projection", "")
	}
	root, err := validateFragmentRoot(spec.RepositoryRoot)
	if err != nil {
		return directwrite.MutationSet{}, nil, err
	}
	directory, lockPath, err := resolveFragmentDestination(spec.ProtoEntry)
	if err != nil {
		return directwrite.MutationSet{}, nil, err
	}
	if len(spec.OutputScopes) != 1 || spec.OutputScopes[0].Mode != directwrite.OutputModeFileSet || spec.OutputScopes[0].Path != directory {
		return directwrite.MutationSet{}, nil, newHostError("resolve-output", "crud_proto_scope_invalid", "/outputScopes", "")
	}

	fragments := projection.state.Fragments()
	desired := make(map[string]struct{}, len(fragments))
	writes := make([]directwrite.OutputFile, 0, len(fragments)+1)
	for _, fragment := range fragments {
		if err := contextError(ctx); err != nil {
			return directwrite.MutationSet{}, nil, err
		}
		key := string(fragment.SchemaKey())
		basename := "ent." + key + ".generated.proto"
		if len(key) > maxSchemaKeyBytes || len(basename) > 255 || !fragmentBasenamePattern.MatchString(basename) {
			return directwrite.MutationSet{}, nil, newHostError("validate-input", "schema_key_invalid", "/projection/fragments", "")
		}
		target := path.Join(directory, basename)
		if _, duplicate := desired[target]; duplicate {
			return directwrite.MutationSet{}, nil, newHostError("validate-input", "schema_key_collision", "/projection/fragments", "")
		}
		desired[target] = struct{}{}
		writes = append(writes, directwrite.OutputFile{Path: target, Content: fragment.ProtoBytes()})
	}

	proposal := projection.state.LockProposal()
	if len(fragments) > 0 {
		after := proposal.After()
		if !proposal.Valid() || !after.Valid() || len(after.CanonicalJSON()) == 0 {
			return directwrite.MutationSet{}, nil, newHostError("validate-input", "lock_proposal_invalid", "/projection/lockProposal/after", "")
		}
		writes = append(writes, directwrite.OutputFile{Path: lockPath, Content: after.CanonicalJSON()})
	}

	deletes, guards, err := scanFragmentDirectory(ctx, root, directory, desired, hooks)
	if err != nil {
		return directwrite.MutationSet{}, nil, err
	}
	sort.Slice(writes, func(i, j int) bool { return writes[i].Path < writes[j].Path })
	sort.Strings(deletes)
	sort.Slice(guards, func(i, j int) bool { return guards[i].path < guards[j].path })
	return directwrite.MutationSet{
		Scopes:  append([]directwrite.OutputScope(nil), spec.OutputScopes...),
		Writes:  writes,
		Deletes: deletes,
	}, guards, nil
}

func scanFragmentDirectory(ctx context.Context, root, directory string, desired map[string]struct{}, hooks fragmentExecutionHooks) ([]string, []guardedFragment, error) {
	dir, err := openFragmentDirectory(root, directory)
	if errors.Is(err, fs.ErrNotExist) {
		return []string{}, absentDesiredGuards(desired), nil
	}
	if errors.Is(err, errFragmentGuardPlatformUnsupported) {
		return nil, nil, fragmentFileError("platform_unsupported", directory)
	}
	if err != nil {
		return nil, nil, fragmentFileError("crud_proto_scope_invalid", directory)
	}
	defer dir.Close()

	deletes := []string{}
	guardsByPath := map[string]guardedFragment{}
	count := 0
	for {
		if err := contextError(ctx); err != nil {
			return nil, nil, err
		}
		entries, readErr := dir.ReadDir(fragmentDirectoryBatchSize)
		for _, entry := range entries {
			if hooks.onScanEntry != nil {
				hooks.onScanEntry(path.Join(directory, entry.Name()))
			}
			if err := contextError(ctx); err != nil {
				return nil, nil, err
			}
			count++
			if count > maxFragmentDirectoryEntries {
				return nil, nil, fragmentFileError("fragment_directory_entry_limit_exceeded", directory)
			}
			if !fragmentBasenamePattern.MatchString(entry.Name()) {
				continue
			}
			target := path.Join(directory, entry.Name())
			file, openErr := openFragmentCandidate(dir, entry.Name())
			if openErr != nil {
				return nil, nil, fragmentFileError("fragment_changed_during_scan", target)
			}
			info, infoErr := file.Stat()
			if infoErr != nil || !info.Mode().IsRegular() {
				file.Close()
				return nil, nil, fragmentFileError("fragment_type_invalid", target)
			}
			if hooks.beforeMarkerRead != nil {
				hooks.beforeMarkerRead(target)
			}
			marked, markerErr := readExactFragmentMarker(ctx, file, target)
			closeErr := file.Close()
			if markerErr != nil {
				return nil, nil, markerErr
			}
			if closeErr != nil {
				return nil, nil, fragmentFileError("fragment_inspection_failed", target)
			}
			_, wanted := desired[target]
			if wanted && !marked {
				return nil, nil, fragmentFileError("generated_fragment_adoption_denied", target)
			}
			if !marked {
				continue
			}
			guardsByPath[target] = guardedFragment{path: target, present: true, info: info}
			if !wanted {
				deletes = append(deletes, target)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, nil, fragmentFileError("fragment_directory_read_failed", directory)
		}
	}
	for target := range desired {
		if _, present := guardsByPath[target]; !present {
			guardsByPath[target] = guardedFragment{path: target, present: false}
		}
	}
	guards := make([]guardedFragment, 0, len(guardsByPath))
	for _, guard := range guardsByPath {
		guards = append(guards, guard)
	}
	return deletes, guards, nil
}

func absentDesiredGuards(desired map[string]struct{}) []guardedFragment {
	guards := make([]guardedFragment, 0, len(desired))
	for target := range desired {
		guards = append(guards, guardedFragment{path: target, present: false})
	}
	return guards
}

func recheckGuardedFragments(ctx context.Context, spec FragmentMutationSpec, guards []guardedFragment, hooks fragmentExecutionHooks) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	directory := spec.OutputScopes[0].Path
	dir, err := openFragmentDirectory(spec.RepositoryRoot, directory)
	if errors.Is(err, fs.ErrNotExist) {
		for _, guard := range guards {
			if guard.present {
				return fragmentFileError("fragment_guard_mismatch", guard.path)
			}
		}
		return nil
	}
	if errors.Is(err, errFragmentGuardPlatformUnsupported) {
		return fragmentFileError("platform_unsupported", directory)
	}
	if err != nil {
		return fragmentFileError("fragment_guard_mismatch", directory)
	}
	defer dir.Close()
	for _, guard := range guards {
		if hooks.onGuardRecheck != nil {
			hooks.onGuardRecheck(guard.path)
		}
		if err := contextError(ctx); err != nil {
			return err
		}
		file, openErr := openFragmentCandidate(dir, path.Base(guard.path))
		if errors.Is(openErr, fs.ErrNotExist) && !guard.present {
			continue
		}
		if openErr != nil {
			return fragmentFileError("fragment_guard_mismatch", guard.path)
		}
		info, infoErr := file.Stat()
		if infoErr != nil || !info.Mode().IsRegular() || !guard.present || !os.SameFile(guard.info, info) {
			file.Close()
			return fragmentFileError("fragment_guard_mismatch", guard.path)
		}
		marked, markerErr := readExactFragmentMarker(ctx, file, guard.path)
		closeErr := file.Close()
		if markerErr != nil {
			return markerErr
		}
		if closeErr != nil || !marked {
			return fragmentFileError("fragment_guard_mismatch", guard.path)
		}
	}
	return nil
}

func readExactFragmentMarker(ctx context.Context, reader io.Reader, source string) (bool, error) {
	want := []byte(generatedFragmentMarker + "\n")
	buffer := make([]byte, len(want))
	offset := 0
	for offset < len(buffer) {
		if err := contextError(ctx); err != nil {
			return false, err
		}
		n, err := reader.Read(buffer[offset:])
		if n > 0 {
			offset += n
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, fragmentFileError("fragment_inspection_failed", source)
		}
		if n == 0 {
			return false, fragmentFileError("fragment_inspection_failed", source)
		}
	}
	return bytes.Equal(buffer, want), nil
}

func resolveFragmentDestination(entry string) (string, string, error) {
	ref, err := provenance.RepositoryRef(entry, "")
	if err != nil || ref.Path() != entry || path.Ext(entry) != ".proto" {
		return "", "", newHostError("resolve-output", "proto_entry_invalid", "/protoEntry", "")
	}
	directory := path.Dir(entry)
	if directory == "." || directory == "/" {
		return "", "", newHostError("resolve-output", "crud_proto_scope_invalid", "/protoEntry", "")
	}
	base := strings.TrimSuffix(path.Base(entry), ".proto")
	return directory, path.Join(directory, base+".crud-protocol.lock.json"), nil
}

func validateFragmentRoot(root string) (string, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", newHostError("resolve-output", "repository_root_invalid", "/repositoryRoot", "")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", newHostError("resolve-output", "repository_root_invalid", "/repositoryRoot", "")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return "", newHostError("resolve-output", "repository_root_invalid", "/repositoryRoot", "")
	}
	return root, nil
}

func contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return newHostCauseError("validate-input", "context_canceled", "/context", "", err)
	}
	return nil
}

func fragmentFileError(reason, source string) error {
	return newHostError("validate-input", reason, "/fragments", source)
}

func (p FragmentProjection) Valid() bool { return p.state.Valid() }
func (p FragmentProjection) Fragments() []ProtoFragment {
	values := p.state.Fragments()
	result := make([]ProtoFragment, len(values))
	for index, value := range values {
		result[index] = ProtoFragment{state: value}
	}
	return result
}
func (p FragmentProjection) EntitySnapshot() []byte { return p.state.EntitySnapshot() }
func (p FragmentProjection) CRUDSnapshot() []byte   { return p.state.CRUDSnapshot() }
func (p FragmentProjection) LockProposal() LockProposal {
	return LockProposal{state: p.state.LockProposal()}
}
func (f ProtoFragment) Valid() bool                        { return f.state.Valid() }
func (f ProtoFragment) SchemaID() SchemaID                 { return f.state.SchemaID() }
func (f ProtoFragment) SchemaKey() SchemaKey               { return f.state.SchemaKey() }
func (f ProtoFragment) ProtoBytes() []byte                 { return f.state.ProtoBytes() }
func (f ProtoFragment) SourceRefs() []provenance.SourceRef { return f.state.SourceRefs() }
