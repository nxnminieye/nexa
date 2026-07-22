package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

const maxBuildFileBytes = 256 << 20

type ArchiveEntry struct {
	SourcePath  string
	ArchivePath string
	Mode        fs.FileMode
}

type ArchiveFile struct {
	Path   string `json:"path"`
	Mode   int64  `json:"mode"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type ArchiveManifest struct {
	SHA256  string        `json:"sha256"`
	Size    int64         `json:"size"`
	Entries []ArchiveFile `json:"entries"`
}

type ScanPolicy struct {
	MaxArchiveBytes  int64
	MaxFiles         int
	MaxFileBytes     int64
	MaxTotalBytes    int64
	ForbiddenContent []string
}

func DefaultScanPolicy() ScanPolicy {
	return ScanPolicy{MaxArchiveBytes: 512 << 20, MaxFiles: 100_000, MaxFileBytes: 256 << 20, MaxTotalBytes: 2 << 30}
}

func BuildArchive(writer io.Writer, root string, entries []ArchiveEntry) (ArchiveManifest, error) {
	if writer == nil || !filepath.IsAbs(root) || len(entries) == 0 {
		return ArchiveManifest{}, fmt.Errorf("archive build input is invalid")
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return ArchiveManifest{}, fmt.Errorf("resolve archive root: %w", err)
	}
	info, err := os.Stat(canonicalRoot)
	if err != nil || !info.IsDir() {
		return ArchiveManifest{}, fmt.Errorf("archive root is not a directory")
	}
	rootHandle, err := os.OpenRoot(canonicalRoot)
	if err != nil {
		return ArchiveManifest{}, fmt.Errorf("open archive root: %w", err)
	}
	defer rootHandle.Close()
	ordered := append([]ArchiveEntry(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ArchivePath < ordered[j].ArchivePath })
	for index, entry := range ordered {
		if !safeArchivePath(entry.SourcePath) || !safeArchivePath(entry.ArchivePath) || !supportedArchiveMode(entry.Mode) ||
			index > 0 && ordered[index-1].ArchivePath == entry.ArchivePath {
			return ArchiveManifest{}, fmt.Errorf("archive entry is invalid")
		}
	}

	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	gzipWriter.Header.ModTime = time.Unix(0, 0).UTC()
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	manifest := ArchiveManifest{Entries: make([]ArchiveFile, 0, len(ordered))}
	for _, entry := range ordered {
		file, err := rootHandle.Open(filepath.FromSlash(entry.SourcePath))
		if err != nil {
			return ArchiveManifest{}, closeArchiveWriters(tarWriter, gzipWriter, fmt.Errorf("open archive source %q: %w", entry.SourcePath, err))
		}
		fileInfo, err := file.Stat()
		if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Mode().Perm() != entry.Mode ||
			fileInfo.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 || fileInfo.Size() > maxBuildFileBytes {
			_ = file.Close()
			return ArchiveManifest{}, closeArchiveWriters(tarWriter, gzipWriter, fmt.Errorf("archive source %q is invalid", entry.SourcePath))
		}
		content, err := io.ReadAll(io.LimitReader(file, maxBuildFileBytes+1))
		closeErr := file.Close()
		if err != nil {
			return ArchiveManifest{}, closeArchiveWriters(tarWriter, gzipWriter, fmt.Errorf("read archive source: %w", err))
		}
		if closeErr != nil {
			return ArchiveManifest{}, closeArchiveWriters(tarWriter, gzipWriter, fmt.Errorf("close archive source: %w", closeErr))
		}
		if len(content) > maxBuildFileBytes || int64(len(content)) != fileInfo.Size() {
			return ArchiveManifest{}, closeArchiveWriters(tarWriter, gzipWriter, fmt.Errorf("archive source size changed while reading"))
		}
		header := &tar.Header{
			Name: entry.ArchivePath, Mode: int64(entry.Mode.Perm()), Size: int64(len(content)), Typeflag: tar.TypeReg,
			ModTime: time.Unix(0, 0).UTC(), AccessTime: time.Time{}, ChangeTime: time.Time{}, Uid: 0, Gid: 0,
			Format: tar.FormatUSTAR,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return ArchiveManifest{}, closeArchiveWriters(tarWriter, gzipWriter, err)
		}
		if _, err := tarWriter.Write(content); err != nil {
			return ArchiveManifest{}, closeArchiveWriters(tarWriter, gzipWriter, err)
		}
		manifest.Entries = append(manifest.Entries, ArchiveFile{
			Path: entry.ArchivePath, Mode: int64(entry.Mode.Perm()), Size: int64(len(content)), SHA256: digestBytes(content),
		})
	}
	if err := tarWriter.Close(); err != nil {
		_ = gzipWriter.Close()
		return ArchiveManifest{}, err
	}
	if err := gzipWriter.Close(); err != nil {
		return ArchiveManifest{}, err
	}
	manifest.Size, manifest.SHA256 = int64(output.Len()), digestBytes(output.Bytes())
	written, err := writer.Write(output.Bytes())
	if err != nil {
		return ArchiveManifest{}, err
	}
	if written != output.Len() {
		return ArchiveManifest{}, io.ErrShortWrite
	}
	return manifest, nil
}

func closeArchiveWriters(tarWriter *tar.Writer, gzipWriter *gzip.Writer, primary error) error {
	_ = tarWriter.Close()
	_ = gzipWriter.Close()
	return primary
}

func ScanArchive(reader io.Reader, policy ScanPolicy) (ArchiveManifest, error) {
	if reader == nil || !validScanPolicy(policy) {
		return ArchiveManifest{}, fmt.Errorf("archive scan input is invalid")
	}
	compressed, err := io.ReadAll(io.LimitReader(reader, policy.MaxArchiveBytes+1))
	if err != nil || int64(len(compressed)) > policy.MaxArchiveBytes {
		return ArchiveManifest{}, fmt.Errorf("archive exceeds scan limit")
	}
	compressedReader := bytes.NewReader(compressed)
	gzipReader, err := gzip.NewReader(compressedReader)
	if err != nil {
		return ArchiveManifest{}, fmt.Errorf("open gzip archive: %w", err)
	}
	gzipReader.Multistream(false)
	tarReader := tar.NewReader(gzipReader)
	report := ArchiveManifest{SHA256: digestBytes(compressed), Size: int64(len(compressed)), Entries: []ArchiveFile{}}
	var total int64
	previous := ""
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = gzipReader.Close()
			return ArchiveManifest{}, fmt.Errorf("read tar header: %w", err)
		}
		if !safeArchivePath(header.Name) || header.Name <= previous || header.Typeflag != tar.TypeReg ||
			!supportedArchiveMode(fs.FileMode(header.Mode)) || header.Size < 0 || header.Size > policy.MaxFileBytes ||
			len(report.Entries) >= policy.MaxFiles || total > policy.MaxTotalBytes-header.Size {
			_ = gzipReader.Close()
			return ArchiveManifest{}, fmt.Errorf("archive entry is unsafe")
		}
		content, err := io.ReadAll(io.LimitReader(tarReader, header.Size+1))
		if err != nil || int64(len(content)) != header.Size {
			_ = gzipReader.Close()
			return ArchiveManifest{}, fmt.Errorf("archive entry content is invalid")
		}
		for _, forbidden := range policy.ForbiddenContent {
			if bytes.Contains(content, []byte(forbidden)) {
				_ = gzipReader.Close()
				return ArchiveManifest{}, fmt.Errorf("archive entry contains forbidden content")
			}
		}
		report.Entries = append(report.Entries, ArchiveFile{
			Path: header.Name, Mode: header.Mode, Size: header.Size, SHA256: digestBytes(content),
		})
		total += header.Size
		previous = header.Name
	}
	trailing, err := io.ReadAll(gzipReader)
	if err != nil {
		_ = gzipReader.Close()
		return ArchiveManifest{}, fmt.Errorf("verify gzip checksum: %w", err)
	}
	if len(trailing) != 0 {
		_ = gzipReader.Close()
		return ArchiveManifest{}, fmt.Errorf("archive contains trailing tar content")
	}
	if err := gzipReader.Close(); err != nil {
		return ArchiveManifest{}, fmt.Errorf("close gzip archive: %w", err)
	}
	if compressedReader.Len() != 0 {
		return ArchiveManifest{}, fmt.Errorf("archive contains trailing compressed input")
	}
	if len(report.Entries) == 0 {
		return ArchiveManifest{}, fmt.Errorf("archive is empty")
	}
	return report, nil
}

func VerifyArchive(reader io.Reader, expected ArchiveManifest, policy ScanPolicy) error {
	actual, err := ScanArchive(reader, policy)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("archive content does not match manifest")
	}
	return nil
}

func validScanPolicy(policy ScanPolicy) bool {
	if policy.MaxArchiveBytes <= 0 || policy.MaxFiles <= 0 || policy.MaxFileBytes <= 0 || policy.MaxTotalBytes <= 0 {
		return false
	}
	for _, value := range policy.ForbiddenContent {
		if value == "" || strings.ContainsRune(value, 0) {
			return false
		}
	}
	return true
}

func supportedArchiveMode(mode fs.FileMode) bool {
	return mode == 0o644 || mode == 0o755
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}
