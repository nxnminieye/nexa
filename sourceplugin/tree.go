package sourceplugin

import (
	"bytes"
	"embed"
	"encoding/binary"
	"io"
	"io/fs"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/nxnminieye/nexa/provenance"
)

const treeDigestDomain = "nexa-source-tree-v1\x00"

type TreeLimits struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

func DefaultTreeLimits() TreeLimits {
	return TreeLimits{MaxFiles: 10_000, MaxFileBytes: 32 << 20, MaxTotalBytes: 512 << 20}
}

type TreeInput struct {
	Path    string
	Content []byte
}

type TreeFile struct {
	path    string
	mode    FileMode
	size    int64
	digest  provenance.Digest
	content []byte
}

func (f TreeFile) Path() string              { return f.path }
func (f TreeFile) Mode() FileMode            { return f.mode }
func (f TreeFile) Size() int64               { return f.size }
func (f TreeFile) Digest() provenance.Digest { return f.digest }
func (f TreeFile) Bytes() []byte {
	if f.content == nil {
		return nil
	}
	return append([]byte{}, f.content...)
}

type Tree struct {
	files  []TreeFile
	index  map[string]int
	digest provenance.Digest
	valid  bool
}

func (t Tree) Len() int { return len(t.files) }

func (t Tree) Files() []TreeFile {
	if !t.valid {
		return nil
	}
	return append([]TreeFile{}, t.files...)
}

func (t Tree) Lookup(filePath string) (TreeFile, bool) {
	if !t.valid {
		return TreeFile{}, false
	}
	index, ok := t.index[filePath]
	if !ok {
		return TreeFile{}, false
	}
	return t.files[index], true
}

func (t Tree) Digest() provenance.Digest { return t.digest }

func NewTree(manifest Manifest, inputs []TreeInput, limits TreeLimits) (Tree, error) {
	return newTree(manifest, inputs, limits, treeInputsBorrowed)
}

type treeInputOwnership uint8

const (
	treeInputsBorrowed treeInputOwnership = iota
	treeInputsOwned
)

func newTree(manifest Manifest, inputs []TreeInput, limits TreeLimits, ownership treeInputOwnership) (Tree, error) {
	return newTreeWithDigester(manifest, inputs, limits, ownership, provenance.SHA256)
}

func newTreeWithDigester(manifest Manifest, inputs []TreeInput, limits TreeLimits, ownership treeInputOwnership, digestContent func([]byte) provenance.Digest) (Tree, error) {
	files, err := validateTreePreflight(manifest, limits, len(inputs), true)
	if err != nil {
		return Tree{}, err
	}

	raw := append([]TreeInput(nil), inputs...)
	sort.SliceStable(raw, func(i, j int) bool { return raw[i].Path < raw[j].Path })
	for index, input := range raw {
		reason, internal := validatePortablePath(input.Path, filePointer(index, "path"))
		if internal != nil {
			return Tree{}, internal
		}
		if reason != "" {
			return Tree{}, newTreeError("source_tree_invalid", "tree_file_path_invalid", filePointer(index, "path"))
		}
	}

	normalized := make([]normalizedTreeInput, len(inputs))
	for index, input := range inputs {
		normalized[index] = normalizedTreeInput{path: input.Path, content: input.Content}
	}
	sort.SliceStable(normalized, func(i, j int) bool { return normalized[i].path < normalized[j].path })
	for index := 1; index < len(normalized); index++ {
		if normalized[index].path == normalized[index-1].path {
			return Tree{}, newTreeError("source_tree_invalid", "tree_file_duplicate", filePointer(index, "path"))
		}
	}

	manifestPaths := filePaths(files)
	inputPaths := make([]string, len(normalized))
	inputByPath := make(map[string]normalizedTreeInput, len(normalized))
	for index, input := range normalized {
		inputPaths[index] = input.path
		inputByPath[input.path] = input
	}
	union := sortedPathUnion(manifestPaths, inputPaths)
	unionIndex := pathIndices(union)
	if _, higher, ok := firstCaseFoldCollision(union); ok {
		return Tree{}, newTreeError("source_tree_invalid", "tree_file_collision", filePointer(unionIndex[higher], "path"))
	}
	if _, higher, ok := firstPrefixCollision(union); ok {
		return Tree{}, newTreeError("source_tree_invalid", "tree_file_collision", filePointer(unionIndex[higher], "path"))
	}

	manifestSet := pathSet(manifestPaths)
	inputSet := pathSet(inputPaths)
	for _, filePath := range union {
		if _, expected := manifestSet[filePath]; expected {
			if _, present := inputSet[filePath]; !present {
				return Tree{}, newTreeError("source_tree_invalid", "tree_file_missing", filePointer(unionIndex[filePath], "path"))
			}
		}
	}
	for _, filePath := range union {
		if _, present := inputSet[filePath]; present {
			if _, expected := manifestSet[filePath]; !expected {
				return Tree{}, newTreeError("source_tree_invalid", "tree_file_extra", filePointer(unionIndex[filePath], "path"))
			}
		}
	}

	for _, file := range files {
		if int64(len(inputByPath[file.Path()].content)) > limits.MaxFileBytes {
			return Tree{}, newTreeError("source_tree_invalid", "tree_file_bytes_exceeded", filePointer(unionIndex[file.Path()], "content"))
		}
	}
	for _, file := range files {
		if int64(len(inputByPath[file.Path()].content)) != file.Size() {
			return Tree{}, newTreeError("source_tree_invalid", "tree_file_size_mismatch", filePointer(unionIndex[file.Path()], "content"))
		}
	}
	for _, file := range files {
		if digestContent(inputByPath[file.Path()].content) != file.Digest() {
			return Tree{}, newTreeError("source_tree_invalid", "tree_file_digest_mismatch", filePointer(unionIndex[file.Path()], "content"))
		}
	}
	var actualTotal int64
	for _, file := range files {
		size := int64(len(inputByPath[file.Path()].content))
		if additionExceedsLimit(actualTotal, size, limits.MaxTotalBytes) {
			return Tree{}, newTreeError("source_tree_invalid", "tree_total_bytes_exceeded", "/files")
		}
		actualTotal += size
	}

	treeFiles := make([]TreeFile, len(files))
	index := make(map[string]int, len(files))
	for fileIndex, file := range files {
		input := inputByPath[file.Path()]
		content := input.content
		if ownership == treeInputsBorrowed {
			content = append([]byte{}, content...)
		} else if content == nil {
			content = []byte{}
		}
		treeFiles[fileIndex] = TreeFile{
			path: file.Path(), mode: file.Mode(), size: file.Size(), digest: file.Digest(),
			content: content,
		}
		index[file.Path()] = fileIndex
	}
	return Tree{files: treeFiles, index: index, digest: digestTreeFiles(treeFiles), valid: true}, nil
}

func LoadEmbeddedTree(manifest Manifest, filesystem embed.FS, prefix string, limits TreeLimits) (Tree, error) {
	return loadEmbeddedTreeFS(manifest, filesystem, prefix, limits)
}

func loadEmbeddedTreeFS(manifest Manifest, filesystem fs.FS, prefix string, limits TreeLimits) (Tree, error) {
	files, err := validateTreePreflight(manifest, limits, 0, false)
	if err != nil {
		return Tree{}, err
	}
	if prefix != "." && (!fs.ValidPath(prefix) || path.Clean(prefix) != prefix) {
		return Tree{}, newTreeLoadError("embedded_prefix_invalid", "/prefix")
	}
	info, statErr := fs.Stat(filesystem, prefix)
	if statErr != nil {
		return Tree{}, newTreeLoadError("embedded_prefix_unavailable", "/prefix")
	}
	if !info.IsDir() {
		return Tree{}, newTreeLoadError("embedded_prefix_not_directory", "/prefix")
	}

	scanner := inventoryScanner{
		filesystem: filesystem,
		maxFiles:   limits.MaxFiles,
		maxDirs:    embeddedDirectoryLimit(files, limits.MaxFiles),
	}
	if scanErr := scanner.scan(prefix, ""); scanErr != nil {
		return Tree{}, scanErr
	}
	sort.Slice(scanner.entries, func(i, j int) bool { return scanner.entries[i].path < scanner.entries[j].path })
	for index := 1; index < len(scanner.entries); index++ {
		if scanner.entries[index].path == scanner.entries[index-1].path {
			return Tree{}, newTreeLoadError("embedded_inventory_read_failed", "/tree")
		}
	}

	manifestPaths := filePaths(files)
	inventoryPaths := make([]string, len(scanner.entries))
	for index, entry := range scanner.entries {
		inventoryPaths[index] = entry.path
	}
	union := sortedPathUnion(manifestPaths, inventoryPaths)
	unionIndex := pathIndices(union)
	for _, entry := range scanner.entries {
		if !entry.regular {
			return Tree{}, newTreeLoadError("embedded_file_non_regular", filePointer(unionIndex[entry.path], "path"))
		}
	}
	manifestSet := pathSet(manifestPaths)
	inventorySet := pathSet(inventoryPaths)
	for _, filePath := range union {
		if _, expected := manifestSet[filePath]; expected {
			if _, present := inventorySet[filePath]; !present {
				return Tree{}, newTreeLoadError("embedded_file_missing", filePointer(unionIndex[filePath], "path"))
			}
		}
	}
	for _, filePath := range union {
		if _, present := inventorySet[filePath]; present {
			if _, expected := manifestSet[filePath]; !expected {
				return Tree{}, newTreeLoadError("embedded_file_extra", filePointer(unionIndex[filePath], "path"))
			}
		}
	}

	inputs := make([]TreeInput, len(files))
	for index, file := range files {
		opened, openErr := filesystem.Open(joinEmbeddedPath(prefix, file.Path()))
		if openErr != nil {
			return Tree{}, newTreeLoadError("embedded_file_read_failed", filePointer(unionIndex[file.Path()], "content"))
		}
		readLimit := file.Size()
		if readLimit > limits.MaxFileBytes {
			readLimit = limits.MaxFileBytes
		}
		if readLimit < math.MaxInt64 {
			readLimit++
		}
		content, readErr := readEmbeddedFile(opened, readLimit)
		_ = opened.Close()
		if readErr != nil {
			return Tree{}, newTreeLoadError("embedded_file_read_failed", filePointer(unionIndex[file.Path()], "content"))
		}
		inputs[index] = TreeInput{Path: file.Path(), Content: content}
	}
	for index, file := range files {
		if int64(len(inputs[index].Content)) != file.Size() {
			return Tree{}, newTreeLoadError("embedded_file_size_mismatch", filePointer(unionIndex[file.Path()], "content"))
		}
	}
	for index, file := range files {
		if provenance.SHA256(inputs[index].Content) != file.Digest() {
			return Tree{}, newTreeLoadError("embedded_file_digest_mismatch", filePointer(unionIndex[file.Path()], "content"))
		}
	}
	return newTree(manifest, inputs, limits, treeInputsOwned)
}

type normalizedTreeInput struct {
	path    string
	content []byte
}

type embeddedInventoryEntry struct {
	path    string
	regular bool
}

type inventoryScanner struct {
	filesystem fs.FS
	maxFiles   int
	maxDirs    int
	fileCount  int
	dirCount   int
	entries    []embeddedInventoryEntry
}

func (s *inventoryScanner) scan(directoryPath, relativeDirectory string) *Error {
	opened, err := s.filesystem.Open(directoryPath)
	if err != nil {
		return newTreeLoadError("embedded_inventory_read_failed", "/tree")
	}
	directory, ok := opened.(fs.ReadDirFile)
	if !ok {
		_ = opened.Close()
		return newTreeLoadError("embedded_inventory_read_failed", "/tree")
	}
	var entries []scannedDirectoryEntry
	for {
		n := s.nextReadDirCount()
		batch, readErr := directory.ReadDir(n)
		remainingFiles := s.maxFiles - s.fileCount
		remainingDirectories := s.maxDirs - s.dirCount
		remainingEntries := remainingFiles
		if remainingDirectories > math.MaxInt-remainingEntries {
			remainingEntries = math.MaxInt
		} else {
			remainingEntries += remainingDirectories
		}
		if len(batch) > remainingEntries {
			_ = opened.Close()
			return newTreeLoadError("embedded_inventory_exceeded", "/tree")
		}
		observed := make([]scannedDirectoryEntry, 0, len(batch))
		batchFiles, batchDirectories := 0, 0
		boundExceeded := false
		for _, entry := range batch {
			name := entry.Name()
			isDirectory := entry.Type().IsDir()
			if isDirectory {
				batchDirectories++
				boundExceeded = boundExceeded || batchDirectories > remainingDirectories
			} else {
				batchFiles++
				boundExceeded = boundExceeded || batchFiles > remainingFiles
			}
			if !boundExceeded {
				observed = append(observed, scannedDirectoryEntry{name: name, entry: entry, directory: isDirectory})
			}
		}
		if boundExceeded {
			_ = opened.Close()
			return newTreeLoadError("embedded_inventory_exceeded", "/tree")
		}
		if readErr != nil && readErr != io.EOF {
			_ = opened.Close()
			return newTreeLoadError("embedded_inventory_read_failed", "/tree")
		}
		if len(batch) == 0 {
			if readErr == io.EOF {
				break
			}
			_ = opened.Close()
			return newTreeLoadError("embedded_inventory_read_failed", "/tree")
		}
		s.fileCount += batchFiles
		s.dirCount += batchDirectories
		entries = append(entries, observed...)
		if readErr == io.EOF {
			break
		}
	}
	_ = opened.Close()
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	for _, entry := range entries {
		if entry.name == "." || entry.name == ".." || !fs.ValidPath(entry.name) || strings.Contains(entry.name, "/") {
			return newTreeLoadError("embedded_inventory_read_failed", "/tree")
		}
		relativePath := entry.name
		if relativeDirectory != "" {
			relativePath = path.Join(relativeDirectory, entry.name)
		}
		reason, internal := validatePortablePath(relativePath, "/tree")
		if internal != nil {
			return internal
		}
		if reason != "" {
			return newTreeLoadError("embedded_inventory_read_failed", "/tree")
		}
		entryInfo, infoErr := entry.entry.Info()
		if infoErr != nil || entryInfo.IsDir() != entry.directory {
			return newTreeLoadError("embedded_inventory_read_failed", "/tree")
		}
		if entry.directory {
			if err := s.scan(joinEmbeddedPath(directoryPath, entry.name), relativePath); err != nil {
				return err
			}
			continue
		}
		s.entries = append(s.entries, embeddedInventoryEntry{path: relativePath, regular: entryInfo.Mode().IsRegular()})
	}
	return nil
}

type scannedDirectoryEntry struct {
	name      string
	entry     fs.DirEntry
	directory bool
}

func (s *inventoryScanner) nextReadDirCount() int {
	fileRemaining := observationRemaining(s.maxFiles, s.fileCount)
	dirRemaining := observationRemaining(s.maxDirs, s.dirCount)
	n := fileRemaining
	if dirRemaining < n {
		n = dirRemaining
	}
	if n > 128 {
		n = 128
	}
	return n
}

func observationRemaining(limit, observed int) int {
	if observed >= limit {
		return 1
	}
	remaining := limit - observed
	if remaining == math.MaxInt {
		return math.MaxInt
	}
	return remaining + 1
}

func readEmbeddedFile(file fs.File, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, io.ErrUnexpectedEOF
	}
	content := make([]byte, 0, minInt64AsInt(limit, math.MaxInt))
	remaining := limit
	noProgress := 0
	for remaining > 0 {
		readSize := minInt64AsInt(remaining, 32<<10)
		start := len(content)
		content = content[:start+readSize]
		n, err := file.Read(content[start:])
		content = content[:start]
		if n < 0 || n > readSize {
			return nil, io.ErrUnexpectedEOF
		}
		if n > 0 {
			content = content[:start+n]
			remaining -= int64(n)
			noProgress = 0
		} else {
			noProgress++
		}
		if err == io.EOF {
			return content, nil
		}
		if err != nil {
			return nil, err
		}
		if noProgress >= 100 {
			return nil, io.ErrNoProgress
		}
	}
	return content, nil
}

func validateTreePreflight(manifest Manifest, limits TreeLimits, inputCount int, checkInputCount bool) ([]File, *Error) {
	if limits.MaxFiles <= 0 {
		return nil, newTreeError("source_tree_invalid", "tree_limit_invalid", "/limits/maxFiles")
	}
	if limits.MaxFileBytes <= 0 {
		return nil, newTreeError("source_tree_invalid", "tree_limit_invalid", "/limits/maxFileBytes")
	}
	if limits.MaxTotalBytes <= 0 {
		return nil, newTreeError("source_tree_invalid", "tree_limit_invalid", "/limits/maxTotalBytes")
	}
	if manifest.APIVersion() != APIVersion || manifest.Kind() != Kind {
		return nil, newTreeError("source_tree_invalid", "manifest_required", "/manifest")
	}
	files := manifest.Files()
	sort.Slice(files, func(i, j int) bool { return files[i].Path() < files[j].Path() })
	if len(files) > limits.MaxFiles {
		return nil, newTreeError("source_tree_invalid", "tree_file_count_exceeded", "/files")
	}
	if checkInputCount && inputCount > limits.MaxFiles {
		return nil, newTreeError("source_tree_invalid", "tree_file_count_exceeded", "/files")
	}
	for index, file := range files {
		if file.Size() > limits.MaxFileBytes {
			return nil, newTreeError("source_tree_invalid", "tree_file_bytes_exceeded", filePointer(index, "content"))
		}
	}
	var total int64
	for _, file := range files {
		if file.Size() < 0 || additionExceedsLimit(total, file.Size(), limits.MaxTotalBytes) {
			return nil, newTreeError("source_tree_invalid", "tree_total_bytes_exceeded", "/files")
		}
		total += file.Size()
	}
	return files, nil
}

func digestTreeFiles(files []TreeFile) provenance.Digest {
	var canonical bytes.Buffer
	canonical.WriteString(treeDigestDomain)
	for _, file := range files {
		for _, value := range []string{file.Path(), string(file.Mode()), strconv.FormatInt(file.Size(), 10), file.Digest().String()} {
			var length [8]byte
			binary.BigEndian.PutUint64(length[:], uint64(len(value)))
			canonical.Write(length[:])
			canonical.WriteString(value)
		}
	}
	return provenance.SHA256(canonical.Bytes())
}

func filePaths(files []File) []string {
	paths := make([]string, len(files))
	for index, file := range files {
		paths[index] = file.Path()
	}
	return paths
}

func sortedPathUnion(left, right []string) []string {
	set := make(map[string]struct{}, len(left)+len(right))
	for _, value := range left {
		set[value] = struct{}{}
	}
	for _, value := range right {
		set[value] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func pathSet(paths []string) map[string]struct{} {
	set := make(map[string]struct{}, len(paths))
	for _, value := range paths {
		set[value] = struct{}{}
	}
	return set
}

func pathIndices(paths []string) map[string]int {
	indices := make(map[string]int, len(paths))
	for index, value := range paths {
		indices[value] = index
	}
	return indices
}

func firstCaseFoldCollision(paths []string) (string, string, bool) {
	firstByFold := make(map[string]string, len(paths))
	var bestLeft, bestRight string
	for _, filePath := range paths {
		folded := foldPortablePath(filePath)
		if first, ok := firstByFold[folded]; ok {
			if bestLeft == "" || first < bestLeft || (first == bestLeft && filePath < bestRight) {
				bestLeft, bestRight = first, filePath
			}
			continue
		}
		firstByFold[folded] = filePath
	}
	return bestLeft, bestRight, bestLeft != ""
}

func firstPrefixCollision(paths []string) (string, string, bool) {
	pathByFold := make(map[string]string, len(paths))
	for _, filePath := range paths {
		pathByFold[foldPortablePath(filePath)] = filePath
	}
	var bestLeft, bestRight string
	for _, filePath := range paths {
		folded := foldPortablePath(filePath)
		for separator := strings.IndexByte(folded, '/'); separator >= 0; {
			if parentPath, ok := pathByFold[folded[:separator]]; ok {
				left, right := parentPath, filePath
				if right < left {
					left, right = right, left
				}
				if bestLeft == "" || left < bestLeft || (left == bestLeft && right < bestRight) {
					bestLeft, bestRight = left, right
				}
			}
			next := strings.IndexByte(folded[separator+1:], '/')
			if next < 0 {
				break
			}
			separator += next + 1
		}
	}
	return bestLeft, bestRight, bestLeft != ""
}

func embeddedDirectoryLimit(files []File, maxFiles int) int {
	parents := make(map[string]struct{})
	for _, file := range files {
		for parent := path.Dir(file.Path()); parent != "."; parent = path.Dir(parent) {
			parents[parent] = struct{}{}
		}
	}
	if maxFiles > math.MaxInt-len(parents)-1 {
		return math.MaxInt
	}
	return len(parents) + maxFiles + 1
}

func additionExceedsLimit(current, value, limit int64) bool {
	return value < 0 || current > limit || value > limit-current
}

func joinEmbeddedPath(base, child string) string {
	if base == "." {
		return child
	}
	return path.Join(base, child)
}

func minInt64AsInt(value int64, maximum int) int {
	if value <= 0 {
		return 0
	}
	if value < int64(maximum) {
		return int(value)
	}
	return maximum
}

func filePointer(index int, field string) string {
	return "/files/" + strconv.Itoa(index) + "/" + field
}
