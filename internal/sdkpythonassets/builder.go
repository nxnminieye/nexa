package sdkpythonassets

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"errors"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

type WheelBuilder interface {
	Build(context.Context, WheelBuildRequest) (WheelBuildOutput, error)
}
type WheelBuildRequest struct {
	RepoRoot, Python, MatrixTarget, Wheelhouse, WorkDir, Out string
	Snapshot                                                 Snapshot
}
type WheelBuildOutput struct{ WheelPath, PythonVersion string }
type BuildRequest struct{ RepoRoot, Python, MatrixTarget, Wheelhouse, WorkDir, Out string }

var pythonVersionPattern = regexp.MustCompile(`^3\.(?:9|12)\.[0-9]+$`)

func (o *Owner) Build(ctx context.Context, request BuildRequest) (BuildResult, error) {
	if !validatePathLexical(request.RepoRoot) {
		return BuildResult{}, ownerError(ReasonRepoRootInvalid, "/repo-root", "input", "read-only")
	}
	if err := validateBuildLexical(request); err != nil {
		return BuildResult{}, err
	}
	root, err := openRepoRoot(request.RepoRoot, "read-only")
	if err != nil {
		return BuildResult{}, err
	}
	defer root.Close()
	if err := requireManagedDirectories(root); err != nil {
		return BuildResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return BuildResult{}, ownerError(ReasonOperationCanceled, "/context", "check", "read-only")
	}
	snapshot, err := o.checkSnapshot(root)
	if err != nil {
		return BuildResult{}, err
	}
	resolved, outRoot, err := resolveBuildRoots(request, root)
	if err != nil {
		return BuildResult{}, err
	}
	defer outRoot.Close()
	pythonReal, err := filepath.EvalSymlinks(resolved.Python)
	if err != nil {
		return BuildResult{}, ownerError(ReasonPythonMissing, "/python", "tool", "read-only")
	}
	info, err := os.Stat(pythonReal)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return BuildResult{}, ownerError(ReasonPythonMissing, "/python", "tool", "read-only")
	}
	for _, root := range []string{resolved.RepoRoot, resolved.Wheelhouse, resolved.WorkDir, resolved.Out} {
		overlap, overlapErr := pathsOverlapByIdentity(root, pythonReal)
		if overlapErr != nil || overlap {
			return BuildResult{}, ownerError(ReasonPythonContainmentInvalid, "/python", "tool", "read-only")
		}
	}
	if o.builder == nil {
		return BuildResult{}, ownerError(ReasonToolMissing, "/tool", "tool", "read-only")
	}
	if checker, ok := o.builder.(interface {
		CheckTools(context.Context, string) error
	}); ok {
		if err := checker.CheckTools(ctx, pythonReal); err != nil {
			if ctx.Err() != nil {
				return BuildResult{}, ownerError(ReasonOperationCanceled, "/context", "tool", "read-only")
			}
			return BuildResult{}, ownerError(ReasonToolMissing, "/tool", "tool", "read-only")
		}
	}
	if err := ctx.Err(); err != nil {
		return BuildResult{}, ownerError(ReasonOperationCanceled, "/context", "tool", "read-only")
	}
	output, err := o.builder.Build(ctx, WheelBuildRequest{RepoRoot: resolved.RepoRoot, Python: pythonReal, MatrixTarget: resolved.MatrixTarget, Wheelhouse: resolved.Wheelhouse, WorkDir: resolved.WorkDir, Out: resolved.Out, Snapshot: snapshot})
	if err != nil {
		if ctx.Err() != nil {
			return BuildResult{}, ownerError(ReasonOperationCanceled, "/context", "build", "read-only")
		}
		return BuildResult{}, ownerError(ReasonWheelBuildFailed, "/wheel", "build", "read-only")
	}
	if err := ctx.Err(); err != nil {
		return BuildResult{}, ownerError(ReasonOperationCanceled, "/context", "build", "read-only")
	}
	if !validWheelBasename(output.WheelPath) || !pythonVersionPattern.MatchString(output.PythonVersion) {
		return BuildResult{}, ownerError(ReasonWheelInvalid, "/wheel", "verify", "read-only")
	}
	wheelData, err := readRegularMode(outRoot, output.WheelPath, 1<<30, 0o644)
	if err != nil || len(wheelData) == 0 {
		return BuildResult{}, ownerError(ReasonWheelInvalid, "/wheel", "verify", "read-only")
	}
	recordDigest, err := verifyWheel(wheelData, snapshot)
	if err != nil {
		if errors.Is(err, errRecordInvalid) {
			return BuildResult{}, ownerError(ReasonRecordInvalid, "/record", "verify", "read-only")
		}
		return BuildResult{}, ownerError(ReasonWheelInvalid, "/wheel", "verify", "read-only")
	}
	return BuildResult{APIVersion: "nexa.dev/sdk-python-assets-build-result/v1", IndexDigest: digestBytes(snapshot.IndexBytes()), BootstrapDigest: digestBytes(snapshot.BootstrapBytes()), MatrixTarget: request.MatrixTarget, PythonVersion: output.PythonVersion, PathBase: "out", WheelPath: output.WheelPath, WheelDigest: digestBytes(wheelData), WheelSize: int64(len(wheelData)), RecordDigest: recordDigest, Roles: snapshot.Roles()}, nil
}

func validateBuildLexical(r BuildRequest) error {
	if !validatePathLexical(r.Python) {
		return ownerError(ReasonPythonPathInvalid, "/python", "input", "read-only")
	}
	if r.MatrixTarget != "darwin-arm64" && r.MatrixTarget != "linux-x86_64" {
		return ownerError(ReasonMatrixTargetInvalid, "/matrix-target", "input", "read-only")
	}
	rows := []struct{ value, reason, pointer string }{{r.Wheelhouse, ReasonWheelhousePathInvalid, "/wheelhouse"}, {r.WorkDir, ReasonWorkDirInvalid, "/work-dir"}, {r.Out, ReasonOutPathInvalid, "/out"}}
	for _, row := range rows {
		if !validatePathLexical(row.value) {
			return ownerError(row.reason, row.pointer, "input", "read-only")
		}
	}
	return nil
}
func resolveBuildRoots(r BuildRequest, repoRoot *os.Root) (BuildRequest, *os.Root, error) {
	rows := []struct{ path, reason, pointer string }{{r.Wheelhouse, ReasonWheelhousePathInvalid, "/wheelhouse"}, {r.WorkDir, ReasonWorkDirInvalid, "/work-dir"}, {r.Out, ReasonOutPathInvalid, "/out"}}
	repoReal, err := filepath.EvalSymlinks(r.RepoRoot)
	if err != nil {
		return BuildRequest{}, nil, ownerError(ReasonRepoRootInvalid, "/repo-root", "input", "read-only")
	}
	pinnedInfo, pinnedErr := repoRoot.Lstat(".")
	realRepoInfo, realRepoErr := os.Stat(repoReal)
	if pinnedErr != nil || realRepoErr != nil || !os.SameFile(pinnedInfo, realRepoInfo) {
		return BuildRequest{}, nil, ownerError(ReasonRepoRootInvalid, "/repo-root", "input", "read-only")
	}
	resolved := r
	resolved.RepoRoot = repoReal
	roots := []string{repoReal}
	resolvedRows := []*string{&resolved.Wheelhouse, &resolved.WorkDir, &resolved.Out}
	for index, row := range rows {
		info, err := os.Lstat(row.path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return BuildRequest{}, nil, ownerError(row.reason, row.pointer, "input", "read-only")
		}
		real, err := filepath.EvalSymlinks(row.path)
		if err != nil {
			return BuildRequest{}, nil, ownerError(row.reason, row.pointer, "input", "read-only")
		}
		realInfo, err := os.Stat(real)
		if err != nil || !realInfo.IsDir() {
			return BuildRequest{}, nil, ownerError(row.reason, row.pointer, "input", "read-only")
		}
		for _, root := range roots {
			overlap, overlapErr := pathsOverlapByIdentity(root, real)
			if overlapErr != nil || overlap {
				return BuildRequest{}, nil, ownerError(row.reason, row.pointer, "input", "read-only")
			}
		}
		*resolvedRows[index] = real
		roots = append(roots, real)
	}
	outRoot, err := openDirectoryRoot(resolved.Out)
	if err != nil {
		return BuildRequest{}, nil, ownerError(ReasonOutPathInvalid, "/out", "input", "read-only")
	}
	return resolved, outRoot, nil
}
func validWheelBasename(path string) bool {
	return len(path) >= 5 && len(path) <= 255 && filepath.Base(path) == path && !strings.Contains(path, "\\") && strings.HasSuffix(path, ".whl")
}

var errRecordInvalid = errors.New("record invalid")

func verifyWheel(data []byte, snapshot Snapshot) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	expectedAssets := map[string][]byte{"nexa/_bootstrap.py": snapshot.BootstrapBytes(), "nexa/_generated/bundle-index.json": snapshot.IndexBytes()}
	for _, role := range snapshot.Roles() {
		expectedAssets["nexa/_generated/"+role.Path] = snapshot.Object(role.ID)
	}
	files := map[string]*zip.File{}
	recordName := ""
	for _, file := range reader.File {
		if !validWheelMemberName(file.Name) || file.FileInfo().IsDir() {
			return "", errors.New("wheel member path invalid")
		}
		if _, dup := files[file.Name]; dup {
			return "", errors.New("duplicate wheel member")
		}
		files[file.Name] = file
		if strings.HasSuffix(file.Name, ".dist-info/RECORD") {
			if !validRecordName(file.Name) {
				return "", errors.New("wheel RECORD path invalid")
			}
			if recordName != "" {
				return "", errRecordInvalid
			}
			recordName = file.Name
		}
	}
	if recordName == "" {
		return "", errRecordInvalid
	}
	distInfo := strings.TrimSuffix(recordName, "/RECORD")
	for _, required := range []string{distInfo + "/METADATA", distInfo + "/WHEEL"} {
		if _, ok := files[required]; !ok {
			return "", errors.New("wheel metadata missing")
		}
	}
	members := make(map[string][]byte, len(files)-1)
	for name, file := range files {
		if name == recordName {
			continue
		}
		content, err := readZipBounded(file, 64<<20)
		if err != nil {
			return "", errors.New("wheel member invalid")
		}
		members[name] = content
	}
	for name, want := range expectedAssets {
		got, ok := members[name]
		if !ok || !bytes.Equal(got, want) {
			return "", errors.New("wheel member drift")
		}
	}
	record, err := readZipBounded(files[recordName], resourceRawBytes)
	if err != nil {
		return "", errRecordInvalid
	}
	if err := verifyRecord(record, recordName, members); err != nil {
		return "", err
	}
	return digestBytes(record), nil
}

func validWheelMemberName(name string) bool {
	if name == "" || !utf8.ValidString(name) || !norm.NFC.IsNormalString(name) || strings.ContainsRune(name, '\x00') ||
		strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") ||
		pathpkg.IsAbs(name) || driveStyleAbsolute(name) || pathpkg.Clean(name) != name {
		return false
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func driveStyleAbsolute(name string) bool {
	return len(name) >= 3 && (name[0] >= 'A' && name[0] <= 'Z' || name[0] >= 'a' && name[0] <= 'z') && name[1] == ':' && name[2] == '/'
}

func validRecordName(name string) bool {
	parts := strings.Split(name, "/")
	if len(parts) != 2 || parts[1] != "RECORD" || !strings.HasSuffix(parts[0], ".dist-info") {
		return false
	}
	identity := strings.TrimSuffix(parts[0], ".dist-info")
	separator := strings.IndexByte(identity, '-')
	if separator <= 0 || separator == len(identity)-1 || strings.LastIndexByte(identity, '-') != separator {
		return false
	}
	return validEscapedWheelComponent(identity[:separator]) && validEscapedWheelComponent(identity[separator+1:])
}

func validEscapedWheelComponent(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '_' {
			return false
		}
	}
	return true
}
func readZipBounded(file *zip.File, limit int) ([]byte, error) {
	if file.UncompressedSize64 > uint64(limit) {
		return nil, errors.New("zip member too large")
	}
	r, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil || len(data) > limit {
		return nil, errors.New("zip member invalid")
	}
	return data, nil
}
func verifyRecord(data []byte, recordName string, expected map[string][]byte) error {
	seen := map[string]bool{}
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = 3
	for {
		parts, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || seen[parts[0]] {
			return errRecordInvalid
		}
		seen[parts[0]] = true
		if parts[0] == recordName {
			if parts[1] != "" || parts[2] != "" {
				return errRecordInvalid
			}
			continue
		}
		want, ok := expected[parts[0]]
		if !ok {
			return errRecordInvalid
		}
		sum := sha256.Sum256(want)
		if parts[1] != "sha256="+base64.RawURLEncoding.EncodeToString(sum[:]) || parts[2] != strconv.Itoa(len(want)) {
			return errRecordInvalid
		}
	}
	if len(seen) != len(expected)+1 || !seen[recordName] {
		return errRecordInvalid
	}
	for name := range expected {
		if !seen[name] {
			return errRecordInvalid
		}
	}
	return nil
}
