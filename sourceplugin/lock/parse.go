package lock

import (
	"errors"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/internal/contract"
	"github.com/nxnminieye/nexa/sourceplugin/release"
	"golang.org/x/text/unicode/norm"
)

type lockDocument struct {
	APIVersion     *string              `json:"apiVersion,omitempty"`
	Kind           *string              `json:"kind,omitempty"`
	Release        *lockReleaseDocument `json:"release,omitempty"`
	ProfileID      *string              `json:"profileId,omitempty"`
	ProfileClosure *[]string            `json:"profileClosure,omitempty"`
	Target         *string              `json:"target,omitempty"`
	TrackedFiles   *[]lockFileDocument  `json:"trackedFiles,omitempty"`
}

type lockReleaseDocument struct {
	ProviderID     *string `json:"providerId,omitempty"`
	ModulePath     *string `json:"modulePath,omitempty"`
	PackagePath    *string `json:"packagePath,omitempty"`
	Version        *string `json:"version,omitempty"`
	ManifestDigest *string `json:"manifestDigest,omitempty"`
	TreeDigest     *string `json:"treeDigest,omitempty"`
}

type lockFileDocument struct {
	Path   *string `json:"path,omitempty"`
	Mode   *string `json:"mode,omitempty"`
	Size   *int64  `json:"size,omitempty"`
	Digest *string `json:"digest,omitempty"`
}

func Parse(source string, data []byte, limits Limits) (Snapshot, error) {
	if !validLockSource(source) {
		return Snapshot{}, lockError(ErrLockInput, "source_lock_invalid", "source_location_invalid", "", StageParse)
	}
	if pointer := validateLimits(limits); pointer != "" {
		return Snapshot{}, withLocation(lockError(ErrLockInput, "source_lock_invalid", "lock_limit_invalid", pointer, StageParse), source, 0, 0)
	}
	if int64(len(data)) > limits.MaxDocumentBytes {
		return Snapshot{}, withLocation(lockError(ErrLockInput, "source_lock_invalid", "document_bytes_exceeded", "", StageParse), source, 0, 0)
	}
	document, err := strictdoc.ParseJSON(source, data)
	if err != nil {
		return Snapshot{}, projectDocumentError(source, err)
	}
	var wire lockDocument
	if err := document.Decode(&wire); err != nil {
		return Snapshot{}, projectDecodedDocumentError(document, source, err)
	}
	if wire.APIVersion == nil || wire.Kind == nil || wire.Release == nil || wire.ProfileID == nil || wire.ProfileClosure == nil || wire.Target == nil || wire.TrackedFiles == nil {
		return Snapshot{}, documentSemanticError(document, source, "document_invalid", "")
	}
	if *wire.APIVersion != APIVersion {
		return Snapshot{}, documentSemanticError(document, source, "version_unsupported", "/apiVersion")
	}
	if *wire.Kind != Kind {
		return Snapshot{}, documentSemanticError(document, source, "kind_invalid", "/kind")
	}
	ref, refErr := refFromDocument(wire.Release)
	if refErr != nil {
		return Snapshot{}, withDocumentLocation(document, source, refErr)
	}
	profileID := *wire.ProfileID
	if !contract.ValidStableID(profileID) {
		return Snapshot{}, documentSemanticError(document, source, "profile_id_invalid", "/profileId")
	}
	closure := append([]string(nil), (*wire.ProfileClosure)...)
	if len(closure) == 0 || len(closure) > limits.MaxProfileClosure {
		return Snapshot{}, documentSemanticError(document, source, "profile_closure_invalid", "/profileClosure")
	}
	seenClosure := make(map[string]struct{}, len(closure))
	for index, id := range closure {
		pointer := "/profileClosure/" + strconv.Itoa(index)
		if !contract.ValidStableID(id) {
			return Snapshot{}, documentSemanticError(document, source, "profile_closure_invalid", pointer)
		}
		if _, duplicate := seenClosure[id]; duplicate {
			return Snapshot{}, documentSemanticError(document, source, "profile_closure_duplicate", pointer)
		}
		seenClosure[id] = struct{}{}
	}
	if closure[len(closure)-1] != profileID {
		return Snapshot{}, documentSemanticError(document, source, "profile_closure_root_mismatch", "/profileClosure")
	}
	target := *wire.Target
	if len(target) > limits.MaxTargetBytes {
		return Snapshot{}, documentSemanticError(document, source, "key_target_invalid", "/target")
	}
	if pathErr := projectPathIssue(contract.ValidatePortablePath(target), "key_target_invalid", "/target", StageParse); pathErr != nil {
		if pathErr.Class() == ErrLockInternal {
			return Snapshot{}, pathErr
		}
		return Snapshot{}, documentSemanticError(document, source, "key_target_invalid", "/target")
	}
	files, filesErr := filesFromDocument(document, source, *wire.TrackedFiles, limits)
	if filesErr != nil {
		return Snapshot{}, filesErr
	}
	snapshot, snapshotErr := newSnapshot(ref, profileID, closure, target, files, source, limits, StageParse)
	if snapshotErr != nil {
		return Snapshot{}, withLocation(snapshotErr, source, 0, 0)
	}
	if string(snapshot.canonical) != string(data) {
		return Snapshot{}, documentSemanticError(document, source, "document_not_canonical", "")
	}
	if source != snapshot.key.RepositoryPath() {
		return Snapshot{}, withLocation(lockError(ErrLockConflict, "source_lock_conflict", "source_key_mismatch", "/source", StageParse), source, 0, 0)
	}
	return snapshot, nil
}

func refFromDocument(document *lockReleaseDocument) (release.Ref, *Error) {
	if document == nil || document.ProviderID == nil || document.ModulePath == nil || document.PackagePath == nil || document.Version == nil || document.ManifestDigest == nil || document.TreeDigest == nil {
		return release.Ref{}, lockError(ErrLockInput, "source_lock_invalid", "document_invalid", "/release", StageParse)
	}
	if issue := contract.ValidateIdentity(*document.ProviderID, *document.ModulePath, *document.PackagePath, *document.Version); issue != nil {
		return release.Ref{}, projectIdentityIssue(issue, StageParse, "/release")
	}
	manifestDigest, manifestErr := provenance.ParseDigest(*document.ManifestDigest)
	if manifestErr != nil {
		return release.Ref{}, lockError(ErrLockInput, "source_lock_invalid", "manifest_digest_invalid", "/release/manifestDigest", StageParse)
	}
	treeDigest, treeErr := provenance.ParseDigest(*document.TreeDigest)
	if treeErr != nil {
		return release.Ref{}, lockError(ErrLockInput, "source_lock_invalid", "tree_digest_invalid", "/release/treeDigest", StageParse)
	}
	ref, err := release.NewRef(release.RefSpec{
		ProviderID: *document.ProviderID, ModulePath: *document.ModulePath, PackagePath: *document.PackagePath, Version: *document.Version,
		ManifestDigest: manifestDigest, TreeDigest: treeDigest,
	})
	if err != nil {
		return release.Ref{}, lockError(ErrLockInternal, "source_lock_internal", "owner_projection_failed", "/release", StageParse)
	}
	return ref, nil
}

func projectIdentityIssue(issue *contract.IdentityIssue, stage Stage, pointer string) *Error {
	if issue == nil {
		return nil
	}
	reason, field := "", ""
	switch {
	case issue.Field == contract.IdentityProviderID && issue.Reason == contract.IdentityProviderIDInvalid:
		reason, field = "provider_id_invalid", "providerId"
	case issue.Field == contract.IdentityModulePath && issue.Reason == contract.IdentityModulePathInvalid:
		reason, field = "module_path_invalid", "modulePath"
	case issue.Field == contract.IdentityPackagePath && issue.Reason == contract.IdentityPackagePathInvalid:
		reason, field = "package_path_invalid", "packagePath"
	case issue.Field == contract.IdentityPackagePath && issue.Reason == contract.IdentityPackageModuleMismatch:
		reason, field = "package_module_mismatch", "packagePath"
	case issue.Field == contract.IdentityVersion && issue.Reason == contract.IdentityVersionInvalid:
		reason, field = "version_invalid", "version"
	case issue.Field == contract.IdentityVersion && issue.Reason == contract.IdentityModuleVersionMismatch:
		reason, field = "module_version_mismatch", "version"
	default:
		return lockError(ErrLockInternal, "source_lock_internal", "owner_projection_failed", pointer, stage)
	}
	return lockError(ErrLockInput, "source_lock_invalid", reason, pointer+"/"+field, stage)
}

type trackedCandidate struct {
	file          BaselineFile
	wire          lockFileDocument
	authoredIndex int
	foldedPath    string
}

type trackedPathAncestor struct {
	foldedPath      string
	minimumAncestor int
}

func compareFoldedPortablePaths(left, right string) int {
	for {
		leftEnd, rightEnd := strings.IndexByte(left, '/'), strings.IndexByte(right, '/')
		leftSegment, rightSegment := left, right
		if leftEnd >= 0 {
			leftSegment = left[:leftEnd]
		}
		if rightEnd >= 0 {
			rightSegment = right[:rightEnd]
		}
		if order := strings.Compare(leftSegment, rightSegment); order != 0 {
			return order
		}
		leftDone, rightDone := leftEnd < 0, rightEnd < 0
		if leftDone || rightDone {
			switch {
			case leftDone && rightDone:
				return 0
			case leftDone:
				return -1
			default:
				return 1
			}
		}
		left, right = left[leftEnd+1:], right[rightEnd+1:]
	}
}

func isFoldedPathDescendant(candidate, ancestor string) bool {
	return len(candidate) > len(ancestor) && strings.HasPrefix(candidate, ancestor) && candidate[len(ancestor)] == '/'
}

func firstTrackedPrefixCollision(candidates []trackedCandidate) (int, bool) {
	order := make([]int, len(candidates))
	for index := range order {
		order[index] = index
	}
	sort.Slice(order, func(i, j int) bool {
		left, right := order[i], order[j]
		if compared := compareFoldedPortablePaths(candidates[left].foldedPath, candidates[right].foldedPath); compared != 0 {
			return compared < 0
		}
		return left < right
	})

	ancestors := make([]trackedPathAncestor, 0, len(candidates))
	bestLeft, bestRight, found := 0, 0, false
	for _, canonicalIndex := range order {
		foldedPath := candidates[canonicalIndex].foldedPath
		for len(ancestors) > 0 && !isFoldedPathDescendant(foldedPath, ancestors[len(ancestors)-1].foldedPath) {
			ancestors = ancestors[:len(ancestors)-1]
		}
		minimumAncestor := canonicalIndex
		if len(ancestors) > 0 {
			other := ancestors[len(ancestors)-1].minimumAncestor
			left, right := canonicalIndex, other
			if left > right {
				left, right = right, left
			}
			if !found || left < bestLeft || (left == bestLeft && right < bestRight) {
				bestLeft, bestRight, found = left, right, true
			}
			if other < minimumAncestor {
				minimumAncestor = other
			}
		}
		ancestors = append(ancestors, trackedPathAncestor{
			foldedPath: foldedPath, minimumAncestor: minimumAncestor,
		})
	}
	return bestRight, found
}

func filesFromDocument(document strictdoc.Document, source string, wire []lockFileDocument, limits Limits) ([]BaselineFile, *Error) {
	if len(wire) > limits.MaxTrackedFiles {
		return nil, documentSemanticError(document, source, "tracked_file_count_exceeded", "/trackedFiles")
	}
	candidates := make([]trackedCandidate, len(wire))
	for index, item := range wire {
		base := "/trackedFiles/" + strconv.Itoa(index)
		if item.Path == nil || item.Mode == nil || item.Size == nil || item.Digest == nil {
			return nil, documentSemanticError(document, source, "document_invalid", base)
		}
		candidates[index] = trackedCandidate{file: BaselineFile{path: *item.Path}, wire: item, authoredIndex: index}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].file.path < candidates[j].file.path })
	for index, candidate := range candidates {
		if issue := contract.ValidatePortablePath(candidate.file.path); issue != nil {
			if pathErr := projectPathIssue(issue, "tracked_file_path_invalid", "/trackedFiles/"+strconv.Itoa(index)+"/path", StageParse); pathErr.Class() == ErrLockInternal {
				return nil, pathErr
			}
			return nil, trackedDocumentError(document, source, "tracked_file_path_invalid", index, candidate.authoredIndex, "path")
		}
	}
	for index := 1; index < len(candidates); index++ {
		if candidates[index].file.path == candidates[index-1].file.path {
			return nil, trackedDocumentError(document, source, "tracked_file_duplicate", index, candidates[index].authoredIndex, "path")
		}
	}
	folded := make(map[string]int, len(candidates))
	bestLeft, bestRight, hasFoldedCollision := 0, 0, false
	for index := range candidates {
		candidate := &candidates[index]
		key := contract.FoldPortablePath(candidate.file.path)
		candidate.foldedPath = key
		if previous, ok := folded[key]; ok {
			if !hasFoldedCollision || previous < bestLeft || previous == bestLeft && index < bestRight {
				bestLeft, bestRight, hasFoldedCollision = previous, index, true
			}
			continue
		}
		folded[key] = index
	}
	if hasFoldedCollision {
		return nil, trackedDocumentError(document, source, "tracked_file_collision", bestRight, candidates[bestRight].authoredIndex, "path")
	}
	conflictRight, hasConflict := firstTrackedPrefixCollision(candidates)
	if hasConflict {
		return nil, trackedDocumentError(document, source, "tracked_file_collision", conflictRight, candidates[conflictRight].authoredIndex, "path")
	}
	for index := range candidates {
		mode := sourceplugin.FileMode(*candidates[index].wire.Mode)
		if mode != sourceplugin.Mode0644 && mode != sourceplugin.Mode0755 {
			return nil, trackedDocumentError(document, source, "tracked_file_mode_invalid", index, candidates[index].authoredIndex, "mode")
		}
		candidates[index].file.mode = mode
	}
	for index := range candidates {
		if *candidates[index].wire.Size < 0 || *candidates[index].wire.Size > maxJCSSafeInteger {
			return nil, trackedDocumentError(document, source, "tracked_file_size_invalid", index, candidates[index].authoredIndex, "size")
		}
		candidates[index].file.size = *candidates[index].wire.Size
	}
	for index := range candidates {
		digest, err := provenance.ParseDigest(*candidates[index].wire.Digest)
		if err != nil {
			return nil, trackedDocumentError(document, source, "tracked_file_digest_invalid", index, candidates[index].authoredIndex, "digest")
		}
		candidates[index].file.digest = digest
	}
	for index, candidate := range candidates {
		if candidate.authoredIndex != index {
			return nil, trackedDocumentError(document, source, "tracked_file_order_invalid", index, candidate.authoredIndex, "path")
		}
	}
	files := make([]BaselineFile, len(candidates))
	for index, candidate := range candidates {
		files[index] = candidate.file
	}
	return files, nil
}

func trackedDocumentError(document strictdoc.Document, source, reason string, canonicalIndex, authoredIndex int, field string) *Error {
	publicPointer := "/trackedFiles/" + strconv.Itoa(canonicalIndex) + "/" + field
	authoredPointer := "/trackedFiles/" + strconv.Itoa(authoredIndex) + "/" + field
	line, column, _ := document.Location(authoredPointer)
	return withLocation(lockError(ErrLockInput, "source_lock_invalid", reason, publicPointer, StageParse), source, line, column)
}

func validateLimits(limits Limits) string {
	maxInt := int64(^uint(0) >> 1)
	switch {
	case limits.MaxDocumentBytes <= 0:
		return "/limits/maxDocumentBytes"
	case limits.MaxDocumentBytes > maxInt-1:
		return "/limits/maxDocumentBytes"
	case limits.MaxProfileClosure <= 0:
		return "/limits/maxProfileClosure"
	case limits.MaxProfileClosure > int(^uint(0)>>1)/4:
		return "/limits/maxProfileClosure"
	case limits.MaxTrackedFiles <= 0:
		return "/limits/maxTrackedFiles"
	case limits.MaxTrackedFiles > int(^uint(0)>>1)/4:
		return "/limits/maxTrackedFiles"
	case limits.MaxTargetBytes <= 0:
		return "/limits/maxTargetBytes"
	case limits.MaxTargetBytes > int(^uint(0)>>1)/2:
		return "/limits/maxTargetBytes"
	default:
		return ""
	}
}

func validLockSource(source string) bool {
	if source == "" || len(source) > 1024 || !utf8.ValidString(source) || !norm.NFC.IsNormalString(source) || source == "." || !fs.ValidPath(source) || contract.PortableVolumePath(source) || !strings.HasSuffix(source, ".json") || strings.ContainsAny(source, "\\\x00") {
		return false
	}
	for _, character := range source {
		if unicode.Is(unicode.Cc, character) || unicode.Is(unicode.Cf, character) {
			return false
		}
	}
	return true
}

func projectDocumentError(source string, err error) *Error {
	var strict *strictdoc.Error
	if !errors.As(err, &strict) {
		return withLocation(lockError(ErrLockInput, "source_lock_invalid", "document_invalid", "", StageParse), source, 0, 0)
	}
	reason := strict.Code
	if reason != "document_unknown_field" && reason != "document_duplicate_key" && reason != "document_trailing_input" {
		reason = "document_invalid"
	}
	pointer := safeLockDocumentPointer(strict.Pointer)
	return withLocation(lockError(ErrLockInput, "source_lock_invalid", reason, pointer, StageParse), source, strict.Line, strict.Column)
}

func projectDecodedDocumentError(document strictdoc.Document, source string, err error) *Error {
	projected := projectDocumentError(source, err)
	var strict *strictdoc.Error
	if !errors.As(err, &strict) || strict == nil || projected.Line() > 0 || projected.Column() > 0 {
		return projected
	}
	line, column, ok := document.Location(strict.Pointer)
	if !ok {
		return projected
	}
	return withLocation(projected, source, line, column)
}

func safeLockDocumentPointer(pointer string) string {
	if pointer == "" {
		return ""
	}
	root, next, ok := nextLockPointerComponent(pointer, 0)
	if !ok {
		return ""
	}
	rootPointer := ""
	switch root {
	case "apiVersion", "kind", "profileId", "target":
		rootPointer = "/" + root
		return rootPointer
	case "release", "profileClosure", "trackedFiles":
		rootPointer = "/" + root
	default:
		return ""
	}
	if next == len(pointer) {
		return rootPointer
	}
	component, next, ok := nextLockPointerComponent(pointer, next)
	if !ok {
		return rootPointer
	}
	switch root {
	case "release":
		switch component {
		case "providerId", "modulePath", "packagePath", "version", "manifestDigest", "treeDigest":
			return rootPointer + "/" + component
		default:
			return rootPointer
		}
	case "profileClosure":
		if !canonicalLockPointerIndex(component) {
			return rootPointer
		}
		return rootPointer + "/" + component
	case "trackedFiles":
		if !canonicalLockPointerIndex(component) {
			return rootPointer
		}
		itemPointer := rootPointer + "/" + component
		if next == len(pointer) {
			return itemPointer
		}
		field, _, ok := nextLockPointerComponent(pointer, next)
		if !ok {
			return itemPointer
		}
		switch field {
		case "path", "mode", "size", "digest":
			return itemPointer + "/" + field
		default:
			return itemPointer
		}
	default:
		return rootPointer
	}
}

func nextLockPointerComponent(pointer string, offset int) (string, int, bool) {
	if offset < 0 || offset >= len(pointer) || pointer[offset] != '/' {
		return "", offset, false
	}
	start := offset + 1
	for index := start; index < len(pointer); index++ {
		switch pointer[index] {
		case '/':
			return pointer[start:index], index, index > start
		case '~':
			if index+1 >= len(pointer) || pointer[index+1] != '0' && pointer[index+1] != '1' {
				return "", offset, false
			}
			index++
		}
	}
	return pointer[start:], len(pointer), len(pointer) > start
}

func canonicalLockPointerIndex(component string) bool {
	if component == "0" {
		return true
	}
	if len(component) == 0 || component[0] < '1' || component[0] > '9' {
		return false
	}
	for index := 1; index < len(component); index++ {
		if component[index] < '0' || component[index] > '9' {
			return false
		}
	}
	value, err := strconv.ParseUint(component, 10, strconv.IntSize)
	return err == nil && value <= uint64(int(^uint(0)>>1)/4)
}

func documentSemanticError(document strictdoc.Document, source, reason, pointer string) *Error {
	line, column, _ := document.Location(pointer)
	return withLocation(lockError(ErrLockInput, "source_lock_invalid", reason, pointer, StageParse), source, line, column)
}

func withDocumentLocation(document strictdoc.Document, source string, err *Error) *Error {
	line, column, _ := document.Location(err.pointer)
	return withLocation(err, source, line, column)
}
