package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type shortArchiveWriter struct{}

func (shortArchiveWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }

func TestBuildAndScanArchiveAreDeterministic(t *testing.T) {
	root := t.TempDir()
	writeReleaseTestFile(t, filepath.Join(root, "LICENSE"), []byte("Apache-2.0\n"), 0o644)
	writeReleaseTestFile(t, filepath.Join(root, "bin", "nexa"), []byte("binary\n"), 0o755)
	entries := []ArchiveEntry{
		{SourcePath: "bin/nexa", ArchivePath: "nexa-v0.1.0/bin/nexa", Mode: 0o755},
		{SourcePath: "LICENSE", ArchivePath: "nexa-v0.1.0/LICENSE", Mode: 0o644},
	}
	var first, second bytes.Buffer
	firstManifest, err := BuildArchive(&first, root, entries)
	if err != nil {
		t.Fatal(err)
	}
	secondManifest, err := BuildArchive(&second, root, entries)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) || !reflect.DeepEqual(firstManifest, secondManifest) {
		t.Fatal("archive output is not deterministic")
	}
	policy := DefaultScanPolicy()
	report, err := ScanArchive(bytes.NewReader(first.Bytes()), policy)
	if err != nil {
		t.Fatal(err)
	}
	if report.SHA256 != firstManifest.SHA256 || report.Size != firstManifest.Size || !reflect.DeepEqual(report.Entries, firstManifest.Entries) {
		t.Fatalf("scan report = %#v; manifest=%#v", report, firstManifest)
	}
	if err := VerifyArchive(bytes.NewReader(first.Bytes()), firstManifest, policy); err != nil {
		t.Fatal(err)
	}
}

func TestScanArchiveRejectsUnsafePathAndForbiddenContent(t *testing.T) {
	var raw bytes.Buffer
	gzipWriter := gzip.NewWriter(&raw)
	gzipWriter.Header.ModTime = time.Unix(0, 0).UTC()
	tarWriter := tar.NewWriter(gzipWriter)
	content := []byte("secret\n")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "../secret", Mode: 0o644, Size: int64(len(content)), ModTime: time.Unix(0, 0).UTC(), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ScanArchive(bytes.NewReader(raw.Bytes()), DefaultScanPolicy()); err == nil {
		t.Fatal("unsafe archive path accepted")
	}

	root := t.TempDir()
	writeReleaseTestFile(t, filepath.Join(root, "config.txt"), []byte("token=private\n"), 0o644)
	var archive bytes.Buffer
	if _, err := BuildArchive(&archive, root, []ArchiveEntry{{SourcePath: "config.txt", ArchivePath: "release/config.txt", Mode: 0o644}}); err != nil {
		t.Fatal(err)
	}
	policy := DefaultScanPolicy()
	policy.ForbiddenContent = []string{"token="}
	if _, err := ScanArchive(bytes.NewReader(archive.Bytes()), policy); err == nil {
		t.Fatal("forbidden archive content accepted")
	}
}

func TestBuildArchiveRejectsSourceSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeReleaseTestFile(t, filepath.Join(outside, "artifact.txt"), []byte("outside\n"), 0o644)
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	_, err := BuildArchive(&archive, root, []ArchiveEntry{{
		SourcePath: "linked/artifact.txt", ArchivePath: "release/artifact.txt", Mode: 0o644,
	}})
	if err == nil {
		t.Fatal("symlinked archive source accepted")
	}
}

func TestArchiveRejectsShortWriteSpecialModeChecksumAndTrailingBytes(t *testing.T) {
	root := t.TempDir()
	writeReleaseTestFile(t, filepath.Join(root, "artifact.txt"), []byte("artifact\n"), 0o644)
	entries := []ArchiveEntry{{SourcePath: "artifact.txt", ArchivePath: "release/artifact.txt", Mode: 0o644}}
	if _, err := BuildArchive(shortArchiveWriter{}, root, entries); err == nil {
		t.Fatal("short archive write accepted")
	}
	var valid bytes.Buffer
	if _, err := BuildArchive(&valid, root, entries); err != nil {
		t.Fatal(err)
	}
	trailing := append(append([]byte(nil), valid.Bytes()...), byte('x'))
	if _, err := ScanArchive(bytes.NewReader(trailing), DefaultScanPolicy()); err == nil {
		t.Fatal("trailing compressed byte accepted")
	}
	corrupt := append([]byte(nil), valid.Bytes()...)
	corrupt[len(corrupt)-1] ^= 0xff
	if _, err := ScanArchive(bytes.NewReader(corrupt), DefaultScanPolicy()); err == nil {
		t.Fatal("corrupt gzip checksum accepted")
	}

	var special bytes.Buffer
	gzipWriter := gzip.NewWriter(&special)
	tarWriter := tar.NewWriter(gzipWriter)
	content := []byte("special\n")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "release/special", Mode: 0o4755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ScanArchive(bytes.NewReader(special.Bytes()), DefaultScanPolicy()); err == nil {
		t.Fatal("special archive mode accepted")
	}
}

func writeReleaseTestFile(t *testing.T, name string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, content, mode); err != nil {
		t.Fatal(err)
	}
}
