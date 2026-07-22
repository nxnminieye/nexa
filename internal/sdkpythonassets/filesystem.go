package sdkpythonassets

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

func validatePathLexical(value string) bool {
	return value != "" && len(value) <= 4096 && utf8.ValidString(value) && norm.NFC.IsNormalString(value) && !strings.ContainsRune(value, '\x00') && filepath.IsAbs(value) && filepath.Clean(value) == value && filepath.IsLocal(filepath.Base(value))
}

func openRepoRoot(name, state string) (*os.Root, error) {
	if !validatePathLexical(name) {
		return nil, ownerError(ReasonRepoRootInvalid, "/repo-root", "input", state)
	}
	info, err := os.Lstat(name)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ownerError(ReasonRepoRootInvalid, "/repo-root", "input", state)
	}
	root, err := os.OpenRoot(name)
	if err != nil {
		return nil, ownerError(ReasonRepoRootInvalid, "/repo-root", "input", state)
	}
	opened, err := root.Lstat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, ownerError(ReasonRepoRootInvalid, "/repo-root", "input", state)
	}
	after, err := os.Lstat(name)
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, after) {
		root.Close()
		return nil, ownerError(ReasonRepoRootInvalid, "/repo-root", "input", state)
	}
	goMod, err := readRegular(root, "go.mod", 65_536)
	if err != nil || !exactModule(goMod) {
		root.Close()
		return nil, ownerError(ReasonRepoRootInvalid, "/repo-root", "input", state)
	}
	return root, nil
}

func openDirectoryRoot(name string) (*os.Root, error) {
	info, err := os.Lstat(name)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("directory root invalid")
	}
	root, err := os.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := root.Lstat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, errors.New("directory root identity changed")
	}
	after, err := os.Lstat(name)
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, after) {
		root.Close()
		return nil, errors.New("directory root identity changed")
	}
	return root, nil
}

func exactModule(data []byte) bool {
	found := ""
	for _, line := range strings.Split(string(data), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "module" {
			if len(fields) != 2 || found != "" {
				return false
			}
			found = fields[1]
		}
	}
	return found == "github.com/nxnminieye/nexa"
}

func validateParentDirectories(root *os.Root, name string) error {
	dir := pathpkg.Dir(filepath.ToSlash(name))
	if dir == "." {
		return nil
	}
	current := ""
	for _, part := range strings.Split(dir, "/") {
		if part == "" || part == "." || part == ".." {
			return errors.New("invalid parent")
		}
		if current == "" {
			current = part
		} else {
			current += "/" + part
		}
		info, err := root.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o755 {
			return errors.New("parent is not a real directory")
		}
	}
	return nil
}

func openRealParent(root *os.Root, name string) (*os.Root, string, error) {
	name = filepath.ToSlash(name)
	if name == "" || pathpkg.IsAbs(name) || pathpkg.Clean(name) != name || name == "." || strings.HasPrefix(name, "../") {
		return nil, "", errors.New("invalid rooted path")
	}
	if err := validateParentDirectories(root, name); err != nil {
		return nil, "", err
	}
	parentName := pathpkg.Dir(name)
	base := pathpkg.Base(name)
	before, err := root.Lstat(parentName)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, "", errors.New("parent is not a real directory")
	}
	parent, err := root.OpenRoot(parentName)
	if err != nil {
		return nil, "", err
	}
	opened, err := parent.Lstat(".")
	if err != nil || !os.SameFile(before, opened) {
		parent.Close()
		return nil, "", errors.New("parent identity changed")
	}
	after, err := root.Lstat(parentName)
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, after) {
		parent.Close()
		return nil, "", errors.New("parent identity changed")
	}
	return parent, base, nil
}

func ensureManagedDirectories(root *os.Root) error {
	for _, rel := range []string{"sdk", "sdk/python", packageRelativeDir, generatedRelativeDir, generatedRelativeDir + "/objects", objectsRelativeDir} {
		if err := validateParentDirectories(root, rel); err != nil {
			return err
		}
		info, err := root.Lstat(rel)
		if os.IsNotExist(err) {
			parent, base, openErr := openRealParent(root, rel)
			if openErr != nil {
				return openErr
			}
			mkdirErr := parent.Mkdir(base, 0o755)
			if mkdirErr == nil {
				dir, openDirErr := parent.Open(base)
				if openDirErr != nil {
					mkdirErr = openDirErr
				} else {
					mkdirErr = dir.Chmod(0o755)
					dir.Close()
				}
			}
			parent.Close()
			if mkdirErr != nil && !os.IsExist(mkdirErr) {
				return mkdirErr
			}
			info, err = root.Lstat(rel)
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o755 {
			return errors.New("managed directory invalid")
		}
	}
	return nil
}

func requireManagedDirectories(root *os.Root) error {
	for _, rel := range []string{"sdk", "sdk/python", packageRelativeDir, generatedRelativeDir, generatedRelativeDir + "/objects", objectsRelativeDir} {
		if err := validateParentDirectories(root, rel); err != nil {
			return err
		}
		info, err := root.Lstat(rel)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o755 {
			return ownerError(ReasonRepoRootInvalid, "/repo-root", "input", "read-only")
		}
	}
	return nil
}

func pathsOverlapByIdentity(a, b string) (bool, error) {
	aInfo, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bInfo, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	if os.SameFile(aInfo, bInfo) {
		return true, nil
	}
	match, err := ancestorMatchesIdentity(filepath.Dir(a), bInfo)
	if err != nil || match {
		return match, err
	}
	return ancestorMatchesIdentity(filepath.Dir(b), aInfo)
}

func ancestorMatchesIdentity(path string, target os.FileInfo) (bool, error) {
	for {
		info, err := os.Stat(path)
		if err != nil {
			return false, err
		}
		if os.SameFile(info, target) {
			return true, nil
		}
		parent := filepath.Dir(path)
		if parent == path {
			return false, nil
		}
		path = parent
	}
}

func readRegular(root *os.Root, name string, limit int) ([]byte, error) {
	return readRegularMode(root, name, limit, 0)
}
func readRegularMode(root *os.Root, name string, limit int, mode os.FileMode) ([]byte, error) {
	parent, base, err := openRealParent(root, name)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	before, err := parent.Lstat(base)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() > int64(limit) || mode != 0 && before.Mode().Perm() != mode {
		return nil, errors.New("resource is not a bounded regular file")
	}
	return readRegularInKnown(parent, base, before, limit, mode)
}
func readRegularIn(parent *os.Root, name string, limit int) ([]byte, error) {
	return readRegularInMode(parent, name, limit, 0)
}
func readRegularInMode(parent *os.Root, name string, limit int, mode os.FileMode) ([]byte, error) {
	before, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() > int64(limit) || mode != 0 && before.Mode().Perm() != mode {
		return nil, errors.New("resource is not a bounded regular file")
	}
	return readRegularInKnown(parent, name, before, limit, mode)
}
func readRegularInKnown(parent *os.Root, name string, before os.FileInfo, limit int, mode os.FileMode) ([]byte, error) {
	file, err := parent.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || mode != 0 && opened.Mode().Perm() != mode {
		return nil, errors.New("resource identity changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil || len(data) > limit {
		return nil, errors.New("resource exceeds limit")
	}
	after, err := parent.Lstat(name)
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, after) {
		return nil, errors.New("resource identity changed")
	}
	return data, nil
}

func createExclusiveIn(parent *os.Root, name string, mode os.FileMode) (*os.File, error) {
	file, err := parent.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(mode); err != nil {
		file.Close()
		parent.Remove(name)
		return nil, err
	}
	return file, nil
}
func createExclusive(root *os.Root, name string, mode os.FileMode) (*os.File, error) {
	parent, base, err := openRealParent(root, name)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	return createExclusiveIn(parent, base, mode)
}
func writeFull(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if n < 0 || n > len(data) {
			return io.ErrShortWrite
		}
		data = data[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
func tempName(parent, prefix string) (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	name := prefix + hex.EncodeToString(raw[:])
	if parent == "." {
		return name, nil
	}
	return parent + "/" + name, nil
}
func createTempIn(parent *os.Root, prefix string, mode os.FileMode) (*os.File, string, error) {
	for range 100 {
		name, err := tempName(".", prefix)
		if err != nil {
			return nil, "", err
		}
		file, err := createExclusiveIn(parent, name, mode)
		if os.IsExist(err) {
			continue
		}
		return file, name, err
	}
	return nil, "", errors.New("temporary name exhausted")
}

func validatePublishTargetIn(parent *os.Root, name string) error {
	info, err := parent.Lstat(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("publish target is not a regular file")
	}
	return nil
}

func verifyPublishedMode(parent *os.Root, name string, want []byte, mode os.FileMode) error {
	for range 64 {
		got, err := readRegularInMode(parent, name, len(want), mode)
		if err == nil {
			if string(got) != string(want) {
				return errors.New("published file differs")
			}
			return nil
		}
		runtime.Gosched()
	}
	return errors.New("published file cannot be read")
}

func atomicReplace(root *os.Root, name string, data []byte) error {
	parent, base, err := openRealParent(root, name)
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := validatePublishTargetIn(parent, base); err != nil {
		return err
	}
	file, tmp, err := createTempIn(parent, ".nexa-assets-", 0o644)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = parent.Remove(tmp)
		}
	}()
	if err := writeFull(file, data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := validatePublishTargetIn(parent, base); err != nil {
		return err
	}
	if err := parent.Rename(tmp, base); err != nil {
		return err
	}
	keep = true
	return verifyPublishedMode(parent, base, data, 0o644)
}

func writeImmutable(root *os.Root, name string, data []byte) (bool, error) {
	if existing, err := readRegularMode(root, name, resourceRawBytes, 0o644); err == nil {
		if string(existing) == string(data) {
			return false, nil
		}
		return false, errors.New("immutable object differs")
	} else if !os.IsNotExist(err) {
		return false, err
	}
	parent, base, err := openRealParent(root, name)
	if err != nil {
		return false, err
	}
	defer parent.Close()
	file, tmp, err := createTempIn(parent, ".nexa-object-", 0o644)
	if err != nil {
		return false, err
	}
	defer parent.Remove(tmp)
	if err := writeFull(file, data); err != nil {
		file.Close()
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	if err := validatePublishTargetIn(parent, base); err != nil {
		return false, err
	}
	if err := parent.Link(tmp, base); err != nil {
		if os.IsExist(err) {
			if verifyPublishedMode(parent, base, data, 0o644) == nil {
				return false, nil
			}
		}
		return false, err
	}
	if err := verifyPublishedMode(parent, base, data, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func digestOrAbsent(root *os.Root, name string) string {
	data, err := readRegularMode(root, name, resourceRawBytes, 0o644)
	if err != nil {
		return AbsentDigest
	}
	return digestBytes(data)
}
