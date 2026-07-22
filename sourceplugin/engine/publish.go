package engine

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
)

const (
	sourceControlPath   = ".nexa/source"
	sourceStagingPrefix = "staging-"
	sourceDetach        = PlanOperation("detach")
)

type repository struct{ root string }

type preparedPublish struct {
	root            string
	previewRoot     string
	stagedOwnership string
}

func (prepared preparedPublish) cleanup() {
	if prepared.root != "" {
		_ = os.RemoveAll(prepared.root)
	}
}

func (e *Engine) preparePlanPublish(repository *repository, plan Plan, newLock lock.VerifiedLock) (preparedPublish, error) {
	if repository == nil || !plan.valid || !plan.CanApply() || plan.operation == PlanNoop {
		return preparedPublish{}, newError(ErrConflict, "source_transaction_conflict", "plan_not_applicable", "/plan", "transaction")
	}
	if !newLock.Release().Equal(plan.selection.release) || newLock.ProfileID() != plan.selection.profileID || newLock.Target() != plan.selection.target {
		return preparedPublish{}, newError(ErrInput, "source_transaction_invalid", "lock_selection_mismatch", "/lock", "transaction")
	}
	root, err := validateRepositoryRoot(repository.root)
	if err != nil {
		return preparedPublish{}, err
	}
	live, err := scanTarget(root, plan.selection.target, e.treeLimits)
	if err != nil {
		return preparedPublish{}, err
	}
	if live.digest != plan.beforeDigest {
		return preparedPublish{}, snapshotChanged("/plan/beforeDigest", "validation")
	}
	final := applyChanges(live.files, plan.changes)
	if err := validatePreviewStates(final); err != nil {
		return preparedPublish{}, err
	}
	if err := ensurePublishDirectory(root, sourceControlPath, 0o700); err != nil {
		return preparedPublish{}, publishFailure("control_create_failed", "transaction", err)
	}
	control, _ := safeJoin(root, sourceControlPath)
	staging, err := os.MkdirTemp(control, sourceStagingPrefix)
	if err != nil {
		return preparedPublish{}, publishFailure("stage_create_failed", "transaction", err)
	}
	prepared := preparedPublish{root: staging, previewRoot: filepath.Join(staging, "preview", "target"), stagedOwnership: filepath.Join(staging, "ownership.json")}
	failed := true
	defer func() {
		if failed {
			prepared.cleanup()
		}
	}()
	if err := os.Chmod(staging, 0o700); err != nil {
		return preparedPublish{}, publishFailure("stage_mode_failed", "transaction", err)
	}
	if err := writePreviewTree(staging, final); err != nil {
		return preparedPublish{}, err
	}
	ownership, err := newLock.CanonicalJSON()
	if err != nil {
		return preparedPublish{}, projectOwnerError(err, "transaction")
	}
	if err := writePublishBytes(prepared.stagedOwnership, ownership, 0o600); err != nil {
		return preparedPublish{}, err
	}
	failed = false
	return prepared, nil
}

func (e *Engine) applyPreparedPlan(ctx context.Context, repository *repository, plan Plan, newLock lock.VerifiedLock, prepared preparedPublish) error {
	if err := validateContext(ctx, "transaction"); err != nil {
		return err
	}
	if repository == nil || prepared.root == "" || prepared.previewRoot == "" || prepared.stagedOwnership == "" {
		return newError(ErrInput, "source_transaction_invalid", "prepared_publish_required", "/prepared", "transaction")
	}
	if err := e.verifyPlanOldLock(ctx, repository, plan); err != nil {
		return err
	}
	live, err := scanTarget(repository.root, plan.selection.target, e.treeLimits)
	if err != nil {
		return err
	}
	if live.digest != plan.beforeDigest {
		return snapshotChanged("/plan/beforeDigest", "transaction")
	}
	if err := validateStagedOwnership(prepared.stagedOwnership, newLock); err != nil {
		return err
	}
	for _, change := range mutablePublishChanges(plan.changes) {
		if err := publishPlanChange(repository.root, plan.selection.target, prepared.previewRoot, change, e.treeLimits); err != nil {
			return err
		}
	}
	after, err := scanTarget(repository.root, plan.selection.target, e.treeLimits)
	if err != nil {
		return err
	}
	if after.digest != plan.afterDigest {
		return snapshotChanged("/plan/afterDigest", "transaction")
	}
	if err := e.verifyPlanOldLock(ctx, repository, plan); err != nil {
		return err
	}
	if err := publishOwnership(repository.root, newLock, prepared.stagedOwnership); err != nil {
		return err
	}
	return verifyInstalledLock(repository.root, newLock, e.lockLimits)
}

func mutablePublishChanges(changes []Change) []Change {
	result := make([]Change, 0, len(changes))
	for _, change := range changes {
		if !statesEqual(change.local, change.result) {
			result = append(result, change)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		left, right := publishOrder(result[i]), publishOrder(result[j])
		if left != right {
			return left < right
		}
		leftDepth := strings.Count(result[i].path, "/")
		rightDepth := strings.Count(result[j].path, "/")
		if left == 3 && leftDepth != rightDepth {
			return leftDepth > rightDepth
		}
		if left == 0 && leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return result[i].path < result[j].path
	})
	return result
}

func publishOrder(change Change) int {
	switch {
	case change.result.typeOf == FileDirectory:
		return 0
	case change.result.typeOf == FileRegular:
		return 1
	case change.result.typeOf == FileAbsent && change.local.typeOf == FileRegular:
		return 2
	default:
		return 3
	}
}

func publishPlanChange(root, target, previewRoot string, change Change, limits sourceplugin.TreeLimits) error {
	current, err := scanTarget(root, target, limits)
	if err != nil {
		return err
	}
	if !statesEqual(stateAt(current.files, change.path), change.local) {
		return snapshotChanged("/plan/changes/"+change.path, "transaction")
	}
	relative := pathpkgJoin(target, change.path)
	destination, err := safeJoin(root, relative)
	if err != nil {
		return err
	}
	if err := ensurePublishDirectory(root, filepath.ToSlash(filepath.Dir(relative)), 0o755); err != nil {
		return publishFailure("target_parent_failed", "transaction", err)
	}
	switch change.result.typeOf {
	case FileDirectory:
		if change.local.typeOf == FileAbsent {
			if err := os.Mkdir(destination, os.FileMode(change.result.mode)); err != nil {
				return publishFailure("target_install_failed", "transaction", err)
			}
		}
		if err := os.Chmod(destination, os.FileMode(change.result.mode)); err != nil {
			return publishFailure("target_install_failed", "transaction", err)
		}
	case FileRegular:
		staged := filepath.Join(previewRoot, filepath.FromSlash(change.path))
		if err := validateStagedFile(staged, change.result); err != nil {
			return err
		}
		if err := os.Rename(staged, destination); err != nil {
			return publishFailure("target_install_failed", "transaction", err)
		}
	case FileAbsent:
		if err := os.Remove(destination); err != nil {
			return publishFailure("target_install_failed", "transaction", err)
		}
	default:
		return newError(ErrConflict, "source_transaction_conflict", "preview_type_unsupported", "/plan/changes/"+change.path, "transaction")
	}
	return nil
}

func validateStagedFile(path string, expected FileState) error {
	info, err := os.Lstat(path)
	if err != nil {
		return publishFailure("stage_write_failed", "transaction", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || uint32(info.Mode().Perm()) != expected.mode || info.Size() != expected.size {
		return publishFailure("stage_write_failed", "transaction", nil)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return publishFailure("stage_write_failed", "transaction", err)
	}
	if !bytes.Equal(content, expected.content) {
		return publishFailure("stage_write_failed", "transaction", nil)
	}
	return nil
}

func validateStagedOwnership(path string, expected lock.VerifiedLock) error {
	canonical, err := expected.CanonicalJSON()
	if err != nil {
		return projectOwnerError(err, "transaction")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return publishFailure("stage_write_failed", "transaction", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 || info.Size() != int64(len(canonical)) {
		return publishFailure("stage_write_failed", "transaction", nil)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return publishFailure("stage_write_failed", "transaction", err)
	}
	if !bytes.Equal(content, canonical) {
		return publishFailure("stage_write_failed", "transaction", nil)
	}
	return nil
}

func publishOwnership(root string, expected lock.VerifiedLock, staged string) error {
	if err := ensurePublishDirectory(root, ".nexa/source/locks", 0o700); err != nil {
		return publishFailure("lock_parent_failed", "transaction", err)
	}
	destination, err := safeJoin(root, expected.Key().RepositoryPath())
	if err != nil {
		return err
	}
	if err := os.Rename(staged, destination); err != nil {
		return publishFailure("lock_install_failed", "transaction", err)
	}
	return nil
}

func (e *Engine) applyDetachPublish(ctx context.Context, repository *repository, snapshot lock.Snapshot) error {
	if err := validateContext(ctx, "detach-transaction"); err != nil {
		return err
	}
	if repository == nil || snapshot.Key().RepositoryPath() == "" || snapshot.Source() != snapshot.Key().RepositoryPath() {
		return newError(ErrInput, "source_transaction_invalid", "detach_lock_invalid", "/lock", "detach-transaction")
	}
	canonical, err := snapshot.CanonicalJSON()
	if err != nil {
		return projectOwnerError(err, "detach-transaction")
	}
	if err := exactOwnershipSnapshot(repository.root, snapshot, canonical); err != nil {
		return err
	}
	path, err := safeJoin(repository.root, snapshot.Key().RepositoryPath())
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return publishFailure("lock_backup_failed", "detach-transaction", err)
	}
	return nil
}

func exactOwnershipSnapshot(root string, snapshot lock.Snapshot, canonical []byte) error {
	absolute, err := safeJoin(root, snapshot.Key().RepositoryPath())
	if err != nil {
		return err
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || int64(len(canonical)) != info.Size() {
		return snapshotChanged("/lock", "detach-transaction")
	}
	current, err := os.ReadFile(absolute)
	if err != nil || !bytes.Equal(current, canonical) {
		return snapshotChanged("/lock", "detach-transaction")
	}
	return nil
}

func (e *Engine) verifyPlanOldLock(ctx context.Context, repository *repository, plan Plan) error {
	key, err := lock.NewKey(plan.selection.release.ProviderID(), plan.selection.target)
	if err != nil {
		return projectOwnerError(err, "transaction")
	}
	baseline, exists, loadErr := e.loadManaged(ctx, repository.root, key)
	if plan.operation == PlanMaterialize {
		if loadErr != nil || exists || plan.oldLockDigest.String() != "" {
			return snapshotChanged("/plan/oldLockDigest", "transaction")
		}
		return nil
	}
	if loadErr != nil || !exists || baseline.verified.Digest() != plan.oldLockDigest {
		return snapshotChanged("/plan/oldLockDigest", "transaction")
	}
	return nil
}

func validatePreviewStates(files map[string]FileState) error {
	for _, state := range files {
		if state.typeOf != FileRegular && state.typeOf != FileDirectory {
			return newError(ErrConflict, "source_transaction_conflict", "preview_type_unsupported", "/plan", "preview")
		}
	}
	return nil
}

func writePreviewTree(stagingRoot string, files map[string]FileState) error {
	previewRoot := filepath.Join(stagingRoot, "preview", "target")
	if err := os.MkdirAll(previewRoot, 0o755); err != nil {
		return publishFailure("preview_create_failed", "transaction", err)
	}
	if err := os.Chmod(previewRoot, 0o755); err != nil {
		return publishFailure("preview_mode_failed", "transaction", err)
	}
	for _, relative := range sortedStatePaths(files) {
		state := files[relative]
		absolute := filepath.Join(previewRoot, filepath.FromSlash(relative))
		if absolute != previewRoot && !strings.HasPrefix(absolute, previewRoot+string(os.PathSeparator)) {
			return publishFailure("preview_path_invalid", "transaction", nil)
		}
		if state.typeOf == FileDirectory {
			if err := os.MkdirAll(absolute, os.FileMode(state.mode)); err != nil {
				return publishFailure("preview_create_failed", "transaction", err)
			}
			if err := os.Chmod(absolute, os.FileMode(state.mode)); err != nil {
				return publishFailure("preview_mode_failed", "transaction", err)
			}
			continue
		}
		if err := writePublishBytes(absolute, state.content, os.FileMode(state.mode)); err != nil {
			return err
		}
	}
	return nil
}

func writePublishBytes(absolute string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return publishFailure("stage_parent_failed", "transaction", err)
	}
	file, err := os.OpenFile(absolute, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return publishFailure("stage_create_failed", "transaction", err)
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return publishFailure("stage_write_failed", "transaction", err)
	}
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return publishFailure("stage_mode_failed", "transaction", err)
	}
	if err := file.Close(); err != nil {
		return publishFailure("stage_write_failed", "transaction", err)
	}
	return nil
}

func ensurePublishDirectory(root, relative string, mode os.FileMode) error {
	if relative == "." || relative == "" {
		return nil
	}
	current := root
	for _, segment := range strings.Split(filepath.ToSlash(relative), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return errors.New("publish directory path invalid")
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("publish parent is not a directory")
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Mkdir(current, mode); err != nil {
			return err
		}
		if err := os.Chmod(current, mode); err != nil {
			return err
		}
	}
	return nil
}

func verifyInstalledLock(root string, expected lock.VerifiedLock, limits lock.Limits) error {
	absolute, err := safeJoin(root, expected.Key().RepositoryPath())
	if err != nil {
		return err
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return publishFailure("installed_lock_read_failed", "transaction", err)
	}
	snapshot, err := lock.Parse(expected.Key().RepositoryPath(), data, limits)
	if err != nil {
		return projectOwnerError(err, "transaction")
	}
	if snapshot.Digest() != expected.Digest() {
		return newError(ErrConflict, "source_transaction_conflict", "installed_lock_mismatch", "/lock", "transaction")
	}
	return nil
}

func sortedStatePaths(files map[string]FileState) []string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		leftDepth, rightDepth := strings.Count(paths[i], "/"), strings.Count(paths[j], "/")
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return paths[i] < paths[j]
	})
	return paths
}

func snapshotChanged(pointer, stage string) error {
	return newError(ErrConflict, "source_transaction_conflict", "source_snapshot_changed", pointer, stage)
}

func publishFailure(reason, stage string, cause error) error {
	return newErrorWithCause(ErrExternal, "source_transaction_failed", reason, "", stage, cause)
}
