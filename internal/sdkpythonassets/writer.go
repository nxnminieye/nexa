package sdkpythonassets

import (
	"context"
	"sort"
)

type WriteRequest struct{ RepoRoot string }

func (o *Owner) Write(ctx context.Context, request WriteRequest) (WriteResult, error) {
	root, err := openRepoRoot(request.RepoRoot, "unchanged")
	if err != nil {
		return WriteResult{}, err
	}
	defer root.Close()
	if err := ctx.Err(); err != nil {
		return WriteResult{}, ownerError(ReasonOperationCanceled, "/context", "write", "unchanged")
	}
	if o == nil || o.bundleErr != nil {
		return WriteResult{}, ownerError(ReasonIOFailed, "/repository", "write", "unchanged")
	}
	if err := ensureManagedDirectories(root); err != nil {
		return WriteResult{}, ownerError(ReasonIOFailed, "/repository", "write", "unchanged")
	}
	before := digestOrAbsent(root, indexRelativePath)
	want := digestBytes(o.bundle.index)
	bootstrapDigest := digestBytes(o.bundle.bootstrap)
	result := WriteResult{APIVersion: "nexa.dev/sdk-python-assets-write-result/v1", Changed: before != want || digestOrAbsent(root, bootstrapRelativePath) != bootstrapDigest, IndexDigest: want, BootstrapDigest: bootstrapDigest, Roles: o.bundle.Roles(), ObjectsWritten: []string{}, ObjectsReused: []string{}}
	for _, role := range o.bundle.roles {
		if err := ctx.Err(); err != nil {
			return WriteResult{}, ownerError(ReasonOperationCanceled, "/context", "write", "unchanged")
		}
		rel := generatedRelativeDir + "/" + role.Path
		created, err := writeImmutable(root, rel, o.bundle.Object(role.ID))
		if err != nil {
			return WriteResult{}, ownerError(ReasonIOFailed, "/objects", "write", "unchanged")
		}
		if created {
			result.Changed = true
			result.ObjectsWritten = append(result.ObjectsWritten, rel)
		} else {
			result.ObjectsReused = append(result.ObjectsReused, rel)
		}
	}
	if err := ctx.Err(); err != nil {
		return WriteResult{}, ownerError(ReasonOperationCanceled, "/context", "write", "unchanged")
	}
	if err := atomicReplace(root, bootstrapRelativePath, o.bundle.bootstrap); err != nil {
		return WriteResult{}, ownerError(ReasonIOFailed, "/bootstrap", "write", "unchanged")
	}
	if err := ctx.Err(); err != nil {
		return WriteResult{}, ownerError(ReasonOperationCanceled, "/context", "write", "unchanged")
	}
	if err := atomicReplace(root, indexRelativePath, o.bundle.index); err != nil {
		return WriteResult{}, ownerError(ReasonIOFailed, "/bundleIndex", "write", "unchanged")
	}
	sort.Strings(result.ObjectsWritten)
	sort.Strings(result.ObjectsReused)
	return result, nil
}
