package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

const planDigestDomain = "nexa-source-plan-v1\x00"

type ChangeAction string

const (
	ChangeAdd           ChangeAction = "add"
	ChangeReplace       ChangeAction = "replace"
	ChangeDelete        ChangeAction = "delete"
	ChangePreserveLocal ChangeAction = "preserve-local"
	ChangeConverged     ChangeAction = "converged"
)

type ConflictReason string

const (
	ConflictLocalCollision               ConflictReason = "local-collision"
	ConflictUpstreamDeletedLocalModified ConflictReason = "upstream-deleted-local-modified"
	ConflictLocalDeletedUpstreamModified ConflictReason = "local-deleted-upstream-modified"
	ConflictMerge                        ConflictReason = "merge-conflict"
	ConflictBinary                       ConflictReason = "binary-conflict"
	ConflictType                         ConflictReason = "type-conflict"
	ConflictMode                         ConflictReason = "mode-conflict"
	ConflictTargetOverlap                ConflictReason = "target-overlap"
)

type Change struct {
	path   string
	action ChangeAction
	old    FileState
	local  FileState
	new    FileState
	result FileState
}

func (c Change) Path() string         { return c.path }
func (c Change) Action() ChangeAction { return c.action }
func (c Change) Old() FileState       { return cloneFileState(c.old) }
func (c Change) Local() FileState     { return cloneFileState(c.local) }
func (c Change) New() FileState       { return cloneFileState(c.new) }
func (c Change) Result() FileState    { return cloneFileState(c.result) }

type Conflict struct {
	path   string
	reason ConflictReason
	old    FileState
	local  FileState
	new    FileState
}

func (c Conflict) Path() string           { return c.path }
func (c Conflict) Reason() ConflictReason { return c.reason }
func (c Conflict) Old() FileState         { return cloneFileState(c.old) }
func (c Conflict) Local() FileState       { return cloneFileState(c.local) }
func (c Conflict) New() FileState         { return cloneFileState(c.new) }

type Plan struct {
	operation     PlanOperation
	selection     Selection
	changes       []Change
	conflicts     []Conflict
	beforeDigest  provenance.Digest
	afterDigest   provenance.Digest
	oldLockDigest provenance.Digest
	digest        provenance.Digest
	canonical     []byte
	valid         bool
}

func (p Plan) Operation() PlanOperation        { return p.operation }
func (p Plan) Selection() Selection            { return p.selection }
func (p Plan) CanApply() bool                  { return p.valid && len(p.conflicts) == 0 }
func (p Plan) Changes() []Change               { return cloneChanges(p.changes) }
func (p Plan) Conflicts() []Conflict           { return cloneConflicts(p.conflicts) }
func (p Plan) BeforeDigest() provenance.Digest { return p.beforeDigest }
func (p Plan) AfterDigest() provenance.Digest  { return p.afterDigest }
func (p Plan) Digest() provenance.Digest       { return p.digest }
func (p Plan) CanonicalJSON() ([]byte, error) {
	if !p.valid {
		return nil, newError(ErrInput, "source_plan_invalid", "plan_required", "", "projection")
	}
	return append([]byte(nil), p.canonical...), nil
}

func cloneChanges(input []Change) []Change {
	result := append([]Change(nil), input...)
	for i := range result {
		result[i].old = cloneFileState(result[i].old)
		result[i].local = cloneFileState(result[i].local)
		result[i].new = cloneFileState(result[i].new)
		result[i].result = cloneFileState(result[i].result)
	}
	return result
}

func cloneConflicts(input []Conflict) []Conflict {
	result := append([]Conflict(nil), input...)
	for i := range result {
		result[i].old = cloneFileState(result[i].old)
		result[i].local = cloneFileState(result[i].local)
		result[i].new = cloneFileState(result[i].new)
	}
	return result
}

func (e *Engine) Plan(ctx context.Context, request PlanRequest) (Plan, error) {
	if err := validateContext(ctx, "plan"); err != nil {
		return Plan{}, err
	}
	if !request.Selection.valid {
		return Plan{}, newError(ErrInput, "source_request_invalid", "selection_required", "/selection", "plan")
	}
	root, err := validateRepositoryRoot(request.RepositoryRoot)
	if err != nil {
		return Plan{}, err
	}
	newResolved, err := e.resolver.Resolve(ctx, request.Selection.release)
	if err != nil {
		return Plan{}, projectOwnerError(err, "plan")
	}
	newClosure, err := newResolved.Manifest().ResolveProfile(request.Selection.profileID)
	if err != nil {
		return Plan{}, projectOwnerError(err, "plan")
	}
	if err := e.resolveRequirements(ctx, newClosure); err != nil {
		return Plan{}, err
	}
	key, keyErr := lock.NewKey(request.Selection.release.ProviderID(), request.Selection.target)
	if keyErr != nil {
		return Plan{}, projectOwnerError(keyErr, "plan")
	}
	baseline, managed, err := e.loadManaged(ctx, root, key)
	if err != nil {
		return Plan{}, err
	}
	local, err := scanTarget(root, request.Selection.target, e.treeLimits)
	if err != nil {
		return Plan{}, err
	}
	overlaps, err := e.targetOverlapConflicts(ctx, root, key)
	if err != nil {
		return Plan{}, err
	}
	operation := PlanMaterialize
	oldLockDigest := provenance.Digest{}
	if managed {
		oldLockDigest = baseline.verified.Digest()
		if baseline.verified.Release().Equal(request.Selection.release) && baseline.verified.ProfileID() == request.Selection.profileID {
			return newPlan(PlanNoop, request.Selection, nil, overlaps, local.digest, local.digest, oldLockDigest, local.files)
		}
		operation = PlanUpgrade
	}
	oldFiles := map[string]FileState{}
	if managed {
		oldFiles = baselineFiles(baseline)
	}
	newFiles := closureFiles(newResolved, newClosure)
	changes, conflicts, err := e.mergeAll(ctx, oldFiles, local.files, newFiles)
	if err != nil {
		return Plan{}, err
	}
	conflicts = append(conflicts, overlaps...)
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].path != conflicts[j].path {
			return conflicts[i].path < conflicts[j].path
		}
		return conflicts[i].reason < conflicts[j].reason
	})
	after := applyChanges(local.files, changes)
	return newPlan(operation, request.Selection, changes, conflicts, local.digest, digestSnapshot(after), oldLockDigest, after)
}

func baselineFiles(baseline managedBaseline) map[string]FileState {
	files := make(map[string]FileState)
	for _, tracked := range baseline.verified.TrackedFiles() {
		file, ok := baseline.resolved.Tree().Lookup(tracked.Path())
		if !ok {
			continue
		}
		files[file.Path()] = treeFileState(file)
		addParentDirectories(files, file.Path())
	}
	return files
}

func closureFiles(resolved release.ResolvedRelease, closure sourceplugin.ProfileClosure) map[string]FileState {
	files := make(map[string]FileState)
	for _, selected := range closure.Files() {
		file, ok := resolved.Tree().Lookup(selected.Path())
		if ok {
			files[file.Path()] = treeFileState(file)
			addParentDirectories(files, file.Path())
		}
	}
	return files
}

func treeFileState(file sourceplugin.TreeFile) FileState {
	mode := uint32(0o644)
	if file.Mode() == sourceplugin.Mode0755 {
		mode = 0o755
	}
	return FileState{typeOf: FileRegular, mode: mode, size: file.Size(), digest: file.Digest(), content: file.Bytes()}
}

func (e *Engine) mergeAll(ctx context.Context, oldFiles, localFiles, newFiles map[string]FileState) ([]Change, []Conflict, error) {
	pathSet := make(map[string]bool, len(oldFiles)+len(localFiles)+len(newFiles))
	for path := range oldFiles {
		pathSet[path] = true
	}
	for path := range localFiles {
		pathSet[path] = true
	}
	for path := range newFiles {
		pathSet[path] = true
	}
	paths := make([]string, 0, len(pathSet))
	for path := range pathSet {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	changes := make([]Change, 0, len(paths))
	conflicts := make([]Conflict, 0)
	for _, path := range paths {
		old := stateAt(oldFiles, path)
		local := stateAt(localFiles, path)
		newState := stateAt(newFiles, path)
		change, conflict, err := e.mergeOne(ctx, path, old, local, newState)
		if err != nil {
			return nil, nil, err
		}
		if conflict.reason != "" {
			conflicts = append(conflicts, conflict)
		} else {
			changes = append(changes, change)
		}
	}
	return changes, conflicts, nil
}

func (e *Engine) mergeOne(ctx context.Context, path string, old, local, newState FileState) (Change, Conflict, error) {
	change := func(action ChangeAction, result FileState) Change {
		return Change{path: path, action: action, old: cloneFileState(old), local: cloneFileState(local), new: cloneFileState(newState), result: cloneFileState(result)}
	}
	conflict := func(reason ConflictReason) Conflict {
		return Conflict{path: path, reason: reason, old: cloneFileState(old), local: cloneFileState(local), new: cloneFileState(newState)}
	}
	if old.typeOf != FileAbsent && newState.typeOf != FileAbsent && old.typeOf != newState.typeOf &&
		(old.typeOf == FileRegular || old.typeOf == FileDirectory) &&
		(newState.typeOf == FileRegular || newState.typeOf == FileDirectory) {
		return Change{}, conflict(ConflictType), nil
	}
	if statesEqual(local, old) {
		switch {
		case statesEqual(newState, old):
			return change(ChangeConverged, newState), Conflict{}, nil
		case newState.typeOf == FileAbsent:
			return change(ChangeDelete, newState), Conflict{}, nil
		case old.typeOf == FileAbsent:
			return change(ChangeAdd, newState), Conflict{}, nil
		default:
			return change(ChangeReplace, newState), Conflict{}, nil
		}
	}
	if statesEqual(newState, old) {
		return change(ChangePreserveLocal, local), Conflict{}, nil
	}
	if statesEqual(local, newState) {
		return change(ChangeConverged, newState), Conflict{}, nil
	}
	if old.typeOf == FileAbsent && newState.typeOf == FileAbsent {
		return change(ChangePreserveLocal, local), Conflict{}, nil
	}
	if old.typeOf == FileAbsent {
		return Change{}, conflict(ConflictLocalCollision), nil
	}
	if newState.typeOf == FileAbsent {
		return Change{}, conflict(ConflictUpstreamDeletedLocalModified), nil
	}
	if local.typeOf == FileAbsent {
		return Change{}, conflict(ConflictLocalDeletedUpstreamModified), nil
	}
	if local.typeOf != FileRegular || newState.typeOf != FileRegular {
		return Change{}, conflict(ConflictType), nil
	}
	if local.mode != newState.mode {
		return Change{}, conflict(ConflictMode), nil
	}
	if !mergeableText(old.content) || !mergeableText(local.content) || !mergeableText(newState.content) {
		return Change{}, conflict(ConflictBinary), nil
	}
	if e.mergeDriver == nil {
		return Change{}, conflict(ConflictMerge), nil
	}
	merged, err := e.mergeDriver.Merge(ctx, TextMergeInput{Old: old.content, Local: local.content, New: newState.content})
	if err != nil {
		var projected *Error
		if errors.As(err, &projected) {
			return Change{}, Conflict{}, projected
		}
		return Change{}, Conflict{}, newError(ErrInternal, "source_engine_internal", "merge_driver_failed", "", "plan")
	}
	if !merged.Clean() {
		return Change{}, conflict(ConflictMerge), nil
	}
	content := merged.Bytes()
	result := FileState{typeOf: FileRegular, mode: newState.mode, size: int64(len(content)), digest: provenance.SHA256(content), content: content}
	return change(ChangeReplace, result), Conflict{}, nil
}

func mergeableText(content []byte) bool {
	return len(content) <= 1<<20 && utf8.Valid(content) && !bytes.ContainsRune(content, 0)
}

func stateAt(files map[string]FileState, path string) FileState {
	if state, ok := files[path]; ok {
		return cloneFileState(state)
	}
	return FileState{typeOf: FileAbsent}
}

func statesEqual(left, right FileState) bool {
	if left.typeOf != right.typeOf {
		return false
	}
	if left.typeOf == FileAbsent {
		return true
	}
	if left.typeOf == FileDirectory {
		return left.mode == right.mode
	}
	return left.mode == right.mode && left.size == right.size && left.digest == right.digest
}

func applyChanges(local map[string]FileState, changes []Change) map[string]FileState {
	result := make(map[string]FileState, len(local)+len(changes))
	for path, state := range local {
		result[path] = cloneFileState(state)
	}
	for _, change := range changes {
		if change.result.typeOf == FileAbsent {
			delete(result, change.path)
		} else {
			result[change.path] = cloneFileState(change.result)
		}
	}
	return result
}

func (e *Engine) targetOverlapConflicts(ctx context.Context, root string, requested lock.Key) ([]Conflict, error) {
	directory, err := safeJoin(root, ".nexa/source/locks")
	if err != nil {
		return nil, err
	}
	entries, readErr := os.ReadDir(directory)
	if os.IsNotExist(readErr) {
		return nil, nil
	}
	if readErr != nil {
		return nil, newError(ErrInput, "source_lock_invalid", "lock_directory_read_failed", "/locks", "plan")
	}
	conflicts := make([]Conflict, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() > e.lockLimits.MaxDocumentBytes {
			return nil, newError(ErrInput, "source_lock_invalid", "lock_file_invalid", "/locks", "plan")
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, newError(ErrInput, "source_lock_invalid", "lock_read_failed", "/locks", "plan")
		}
		source := filepath.ToSlash(filepath.Join(".nexa/source/locks", entry.Name()))
		snapshot, parseErr := lock.Parse(source, data, e.lockLimits)
		if parseErr != nil {
			return nil, projectOwnerError(parseErr, "plan")
		}
		if snapshot.Key().Equal(requested) {
			continue
		}
		if _, exists, verifyErr := e.loadManaged(ctx, root, snapshot.Key()); verifyErr != nil || !exists {
			if verifyErr != nil {
				return nil, verifyErr
			}
			return nil, newError(ErrInternal, "source_engine_internal", "lock_disappeared", "", "plan")
		}
		if targetsOverlap(requested.Target(), snapshot.Target()) {
			conflicts = append(conflicts, Conflict{path: snapshot.Target(), reason: ConflictTargetOverlap})
		}
	}
	return conflicts, nil
}

func targetsOverlap(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

type planFileProjection struct {
	Type   FileType `json:"type"`
	Mode   uint32   `json:"mode,omitempty"`
	Size   int64    `json:"size,omitempty"`
	Digest string   `json:"digest,omitempty"`
}

type planChangeProjection struct {
	Path   string             `json:"path"`
	Action ChangeAction       `json:"action"`
	Result planFileProjection `json:"result"`
}

type planConflictProjection struct {
	Path   string         `json:"path"`
	Reason ConflictReason `json:"reason"`
}

type planProjection struct {
	APIVersion    string                                                                                                       `json:"apiVersion"`
	Kind          string                                                                                                       `json:"kind"`
	Operation     PlanOperation                                                                                                `json:"operation"`
	Selection     struct{ ProviderID, ModulePath, PackagePath, Version, ManifestDigest, TreeDigest, ProfileID, Target string } `json:"selection"`
	CanApply      bool                                                                                                         `json:"canApply"`
	Changes       []planChangeProjection                                                                                       `json:"changes"`
	Conflicts     []planConflictProjection                                                                                     `json:"conflicts"`
	BeforeDigest  string                                                                                                       `json:"beforeDigest"`
	AfterDigest   string                                                                                                       `json:"afterDigest"`
	OldLockDigest string                                                                                                       `json:"oldLockDigest,omitempty"`
}

func newPlan(operation PlanOperation, selection Selection, changes []Change, conflicts []Conflict, before, after, oldLockDigest provenance.Digest, _ map[string]FileState) (Plan, error) {
	projection := planProjection{APIVersion: "nexa.dev/source-plan/v1", Kind: "SourcePlan", Operation: operation, CanApply: len(conflicts) == 0, BeforeDigest: before.String(), AfterDigest: after.String(), OldLockDigest: oldLockDigest.String()}
	ref := selection.release
	projection.Selection.ProviderID, projection.Selection.ModulePath, projection.Selection.PackagePath, projection.Selection.Version = ref.ProviderID(), ref.ModulePath(), ref.PackagePath(), ref.Version()
	projection.Selection.ManifestDigest, projection.Selection.TreeDigest = ref.ManifestDigest().String(), ref.TreeDigest().String()
	projection.Selection.ProfileID, projection.Selection.Target = selection.profileID, selection.target
	projection.Changes = make([]planChangeProjection, len(changes))
	for index, change := range changes {
		projection.Changes[index] = planChangeProjection{Path: change.path, Action: change.action, Result: projectFile(change.result)}
	}
	projection.Conflicts = make([]planConflictProjection, len(conflicts))
	for index, conflict := range conflicts {
		projection.Conflicts[index] = planConflictProjection{Path: conflict.path, Reason: conflict.reason}
	}
	canonical, err := json.Marshal(projection)
	if err != nil {
		return Plan{}, newError(ErrInternal, "source_engine_internal", "canonicalization_failed", "", "plan")
	}
	canonical = append(canonical, '\n')
	h := sha256.New()
	_, _ = h.Write([]byte(planDigestDomain))
	_, _ = h.Write(canonical)
	digest, err := provenance.ParseDigest("sha256:" + hex.EncodeToString(h.Sum(nil)))
	if err != nil {
		return Plan{}, newError(ErrInternal, "source_engine_internal", "canonicalization_failed", "", "plan")
	}
	return Plan{operation: operation, selection: selection, changes: cloneChanges(changes), conflicts: cloneConflicts(conflicts), beforeDigest: before, afterDigest: after, oldLockDigest: oldLockDigest, digest: digest, canonical: canonical, valid: true}, nil
}

func projectFile(file FileState) planFileProjection {
	return planFileProjection{Type: file.typeOf, Mode: file.mode, Size: file.size, Digest: file.digest.String()}
}
