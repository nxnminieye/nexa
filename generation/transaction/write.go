package transaction

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/provenance"
)

var errStageCurrentChanged = errors.New("repository changed while staging")

type stagedChange struct {
	target, stage string
	priorDigest   provenance.Digest
	expectsPrior  bool
	hadPrior      bool
	delete        bool
	manual        bool
}

type stagedBundle struct {
	publishes []stagedChange
	manifest  string
}

func Write(ctx context.Context, plan Plan, repositoryPath string, options WriteOptions) (Result, error) {
	// Generation in one worktree is intentionally caller-serialized. Each
	// invocation stages and validates a complete candidate from current state.
	if plan.state == nil {
		return Result{}, writeError("plan_invalid", "/plan")
	}
	if !validDigest(options.PlanDigest) || options.PlanDigest != plan.state.planDigest {
		return Result{}, writeError("plan_digest_mismatch", "/planDigest")
	}
	if len(plan.state.conflicts) != 0 {
		return Result{}, writeError("plan_conflict", "/plan/conflicts")
	}
	if ctx.Err() != nil {
		return Result{}, writeCauseError("cancelled", "", ctx.Err())
	}
	canonicalRepositoryPath, err := canonicalWriteRepositoryRoot(repositoryPath)
	if err != nil {
		return Result{}, writeCauseError("repository_invalid", "/repository", err)
	}
	if canonicalRepositoryPath != plan.state.session.CanonicalRepositoryRoot() {
		return Result{}, writeError("repository_invalid", "/repository")
	}
	bundle := plannedBundle(plan.state)
	if err := validateStagedBundle(ctx, plan.state); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Result{}, writeCauseError("cancelled", "", err)
		}
		return Result{}, writeCauseError("stage_failed", "/staging", err)
	}
	if err := revalidatePlanSources(ctx, plan.state); err != nil {
		return Result{}, err
	}
	root, err := os.OpenRoot(canonicalRepositoryPath)
	if err != nil {
		return Result{}, writeCauseError("repository_invalid", "/repository", err)
	}
	defer root.Close()

	applied, err := planAlreadyApplied(plan.state, root)
	if err != nil {
		return Result{}, writeCauseError("current_changed_after_plan", "/repository", err)
	}
	if applied {
		return cleanResult(plan.state.planDigest, "write")
	}
	if err := verifyPlannedState(plan.state, root); err != nil {
		return Result{}, err
	}
	if err := prepareStagedBundle(root, &bundle); err != nil {
		if errors.Is(err, errStageCurrentChanged) {
			return Result{}, writeCauseError("current_changed_after_plan", "/repository", err)
		}
		return Result{}, writeCauseError("stage_failed", "/staging", err)
	}
	if ctx.Err() != nil {
		return Result{}, writeCauseError("cancelled", "", ctx.Err())
	}

	for index := range bundle.publishes {
		if ctx.Err() != nil {
			return Result{}, writeCauseError("cancelled", "", ctx.Err())
		}
		if err := applyStagedChange(root, &bundle.publishes[index]); err != nil {
			if errors.Is(err, errStageCurrentChanged) {
				return Result{}, writeCauseError("current_changed_after_plan", "/repository", err)
			}
			return Result{}, writeCauseError("commit_failed", "/staging", err)
		}
	}
	return cleanResult(plan.state.planDigest, "write")
}

func canonicalWriteRepositoryRoot(repositoryPath string) (string, error) {
	repository, err := filepath.Abs(repositoryPath)
	if err != nil {
		return "", err
	}
	repository, err = filepath.EvalSymlinks(filepath.Clean(repository))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(repository)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", os.ErrInvalid
	}
	return repository, nil
}

func plannedBundle(state *planState) stagedBundle {
	bundle := stagedBundle{publishes: make([]stagedChange, 0, len(state.changes)+1)}
	for _, change := range state.changes {
		entry := stagedChange{target: change.path, delete: change.kind == ChangeDelete, manual: change.kind == ChangeCreateManual, expectsPrior: change.hasPrior, priorDigest: change.prior}
		if !entry.delete {
			entry.stage = path.Join(state.stagingRoot, change.path)
		}
		bundle.publishes = append(bundle.publishes, entry)
	}
	bundle.manifest = path.Join(state.stagingRoot, state.manifestPath)
	entry := stagedChange{target: state.manifestPath, stage: bundle.manifest, expectsPrior: state.hasPrevious}
	if state.hasPrevious {
		previous, _ := state.previous.CanonicalJSON()
		entry.priorDigest = provenance.SHA256(previous)
	}
	bundle.publishes = append(bundle.publishes, entry)
	return bundle
}

func prepareStagedBundle(root *os.Root, bundle *stagedBundle) error {
	for index := range bundle.publishes {
		if err := prepareStagedChange(root, &bundle.publishes[index]); err != nil {
			return err
		}
	}
	return nil
}

func validateStagedBundle(ctx context.Context, state *planState) error {
	nextArtifacts := make(map[string]artifact.Artifact, len(state.next.Artifacts()))
	for _, item := range state.next.Artifacts() {
		nextArtifacts[item.ID()] = item
	}
	for _, input := range state.inputs {
		if err := ctx.Err(); err != nil {
			return err
		}
		content, err := state.session.Read(input.path)
		if err != nil || provenance.SHA256(content) != input.digest {
			return errors.Join(errors.New("staged artifact digest mismatch"), err)
		}
		if input.createManual || input.overwriteManual {
			continue
		}
		item, ok := nextArtifacts[input.id]
		if !ok || item.Path() != input.path || item.Digest() != input.digest {
			return errors.New("staged artifact does not match manifest")
		}
		if input.probe != nil {
			owned, probeErr := input.probe.Inspect(input.path, content, Ownership{
				GeneratorID: state.next.Generator().ID(), ArtifactID: item.ID(), InputDigest: state.next.InputDigest(),
			})
			if probeErr != nil || !owned {
				return errors.Join(errors.New("staged artifact ownership rejected"), probeErr)
			}
		}
	}
	for _, control := range state.controls {
		if err := ctx.Err(); err != nil {
			return err
		}
		content, err := state.session.Read(control.path)
		if err != nil || provenance.SHA256(content) != control.afterDigest {
			return errors.Join(errors.New("staged control source digest mismatch"), err)
		}
	}
	manifest, err := state.session.Read(state.manifestPath)
	if err != nil {
		return err
	}
	expected, err := state.next.CanonicalJSON()
	if err != nil || provenance.SHA256(manifest) != provenance.SHA256(expected) {
		return errors.New("staged manifest mismatch")
	}
	return nil
}

func revalidatePlanSources(ctx context.Context, state *planState) error {
	if state.revalidateSources == nil {
		if len(state.sources) == 0 {
			return nil
		}
		return writeError("current_changed_after_plan", "/sources")
	}
	current, err := state.revalidateSources(ctx)
	if err != nil {
		return writeCauseError("current_changed_after_plan", "/sources", err)
	}
	normalized, _, err := normalizePlanSources(current)
	if err != nil {
		return writeCauseError("current_changed_after_plan", "/sources", err)
	}
	if len(normalized) != len(state.sources) {
		return writeError("current_changed_after_plan", "/sources")
	}
	for index := range normalized {
		if normalized[index] != state.sources[index] {
			return writeError("current_changed_after_plan", "/sources")
		}
	}
	return nil
}

func prepareStagedChange(root *os.Root, entry *stagedChange) error {
	if err := ensureRootParents(root, entry.target, false, nil); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	info, err := root.Lstat(entry.target)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errStageCurrentChanged
		}
		prior, err := readRootFile(root, entry.target)
		if err != nil {
			return err
		}
		entry.hadPrior = true
		actual := provenance.SHA256(prior)
		if !entry.expectsPrior || actual != entry.priorDigest {
			return errStageCurrentChanged
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else if entry.expectsPrior {
		return errStageCurrentChanged
	}
	return nil
}

func applyStagedChange(root *os.Root, entry *stagedChange) error {
	if err := ensureRootParents(root, entry.target, true, nil); err != nil {
		return err
	}
	if err := verifyEntryPrecondition(root, entry); err != nil {
		return err
	}
	if entry.delete {
		if err := root.Remove(entry.target); err != nil {
			return err
		}
		return nil
	}
	if entry.manual || !entry.hadPrior {
		if err := root.Link(entry.stage, entry.target); err != nil {
			if errors.Is(err, os.ErrExist) {
				return errStageCurrentChanged
			}
			return err
		}
		if err := root.Remove(entry.stage); err != nil {
			return err
		}
	} else {
		if err := root.Rename(entry.stage, entry.target); err != nil {
			return err
		}
	}
	return nil
}

func verifyEntryPrecondition(root *os.Root, entry *stagedChange) error {
	if !entry.hadPrior {
		if _, err := root.Lstat(entry.target); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return errStageCurrentChanged
	}
	matched, err := rootFileMatches(root, entry.target, entry.priorDigest)
	if err != nil || !matched {
		return errStageCurrentChanged
	}
	return nil
}

func ensureRootParents(root *os.Root, target string, create bool, created *[]string) error {
	directory := path.Dir(target)
	if directory == "." {
		return nil
	}
	current := ""
	for _, component := range strings.Split(directory, "/") {
		if component == "" || component == "." || component == ".." {
			return os.ErrInvalid
		}
		current = path.Join(current, component)
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && create {
			if err := root.Mkdir(current, 0o755); err != nil {
				return err
			}
			if created != nil {
				*created = append(*created, current)
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return os.ErrInvalid
		}
	}
	return nil
}

func readRootFile(root *os.Root, name string) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	return data, errors.Join(readErr, closeErr)
}

func rootFileMatches(root *os.Root, name string, expected provenance.Digest) (bool, error) {
	info, err := root.Lstat(name)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, err
	}
	data, err := readRootFile(root, name)
	return err == nil && provenance.SHA256(data) == expected, err
}

func cleanResult(digest provenance.Digest, stage string) (Result, error) {
	state := &resultState{planDigest: digest, changes: []changeState{}, conflicts: []conflictState{}}
	if err := finalizeResult(state, stage); err != nil {
		return Result{}, err
	}
	return Result{state: state}, nil
}

func verifyPlannedState(state *planState, root *os.Root) error {
	changes, conflicts, err := evaluateArtifacts(state, root, "write")
	if err != nil {
		return writeCauseError("current_changed_after_plan", "/artifacts", err)
	}
	controlChanges, controlConflicts, err := evaluateControls(state, root, "build")
	if err != nil {
		return writeCauseError("current_changed_after_plan", "/controlSources", err)
	}
	changes = append(changes, controlChanges...)
	conflicts = append(conflicts, controlConflicts...)
	sortPlanFindings(changes, conflicts)
	if !sameChanges(changes, state.changes) || !sameConflicts(conflicts, state.conflicts) || len(conflicts) != 0 {
		return writeError("current_changed_after_plan", "/repository")
	}
	return verifyManifestBaseline(state, root)
}

func verifyManifestBaseline(state *planState, root *os.Root) error {
	if state.hasPrevious {
		previous, err := state.previous.CanonicalJSON()
		if err != nil {
			return writeCauseError("current_changed_after_plan", "/manifest", err)
		}
		exists, matches, unsafe, err := inspectCurrentArtifact(root, state.manifestPath, previous)
		if err != nil {
			return writeCauseError("current_changed_after_plan", "/manifest", err)
		}
		if unsafe || !exists || !matches {
			return writeError("current_changed_after_plan", "/manifest")
		}
		return nil
	}
	if _, err := root.Lstat(state.manifestPath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return writeCauseError("current_changed_after_plan", "/manifest", err)
	}
	return writeError("current_changed_after_plan", "/manifest")
}

func planAlreadyApplied(state *planState, root *os.Root) (bool, error) {
	for _, input := range state.inputs {
		content, candidateErr := state.session.Read(input.path)
		if candidateErr != nil {
			return false, candidateErr
		}
		if input.createManual {
			if _, err := root.Lstat(input.path); err == nil {
				continue
			} else if errors.Is(err, os.ErrNotExist) {
				return false, nil
			} else {
				return false, err
			}
		}
		if input.overwriteManual {
			exists, matches, unsafe, err := inspectCurrentArtifact(root, input.path, content)
			if err != nil || unsafe || !exists || !matches {
				return false, err
			}
			continue
		}
		exists, matches, unsafe, err := inspectCurrentArtifact(root, input.path, content)
		if err != nil || unsafe || !exists || !matches {
			return false, err
		}
	}
	for _, control := range state.controls {
		exists, matches, unsafe, err := inspectCurrentArtifact(root, control.path, control.after)
		if err != nil || unsafe || !exists || !matches {
			return false, err
		}
	}
	for _, change := range state.changes {
		if change.kind == ChangeDelete {
			if _, err := root.Lstat(change.path); !errors.Is(err, os.ErrNotExist) {
				return false, err
			}
		}
	}
	next, err := state.next.CanonicalJSON()
	if err != nil {
		return false, err
	}
	exists, matches, unsafe, err := inspectCurrentArtifact(root, state.manifestPath, next)
	return err == nil && exists && matches && !unsafe, err
}

func sameChanges(left, right []changeState) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sameConflicts(left, right []conflictState) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
