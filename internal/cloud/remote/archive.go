package remote

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"apm/internal/cloud"
)

const (
	maxArchiveFileSize = int64(10 << 20)
	maxArchiveTreeSize = int64(100 << 20)
	maxArchiveNodes    = 1000
	maxArchiveDepth    = 16
	maxArchivePathSize = 255
	maxArchiveRatio    = int64(100)
	archivePrefixSize  = 4096
)

var errArchiveDecompressedLimit = errors.New("remote archive decompressed byte limit exceeded")

type archiveNode struct {
	name      string
	directory bool
	mode      fs.FileMode
	size      int64
	explicit  bool
}

type archiveManifest struct {
	headers []archiveNode
	nodes   map[string]archiveNode
	size    int64
}

type archiveDestination struct {
	parent   *pinnedDirectory
	name     string
	existing *pinnedEmptyDirectory
}

type archiveHooks struct {
	beforeInspect          func()
	afterInspect           func()
	beforePublish          func()
	afterDestinationRemove func()
	closeFile              func(*os.File) error
}

func (destination *archiveDestination) close() error {
	if destination == nil {
		return nil
	}
	var resultErr error
	if destination.existing != nil {
		resultErr = errors.Join(resultErr, destination.existing.close())
		destination.existing = nil
	}
	if destination.parent != nil {
		resultErr = errors.Join(resultErr, destination.parent.close())
		destination.parent = nil
	}
	return resultErr
}

func prepareArchiveDestination(destination string) (*archiveDestination, error) {
	if destination == "" || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return nil, unsafeRemoteFilesystem("remote archive destination %q must be an absolute clean path", destination)
	}
	name := filepath.Base(destination)
	if !validSecureEntryName(name) {
		return nil, unsafeRemoteFilesystem("remote archive destination %q has invalid base name", destination)
	}
	parent, err := openPinnedDirectory(filepath.Dir(destination))
	if err != nil {
		return nil, fmt.Errorf("open remote archive destination parent: %w", err)
	}
	existing, err := parent.openOptionalEmpty(name)
	if err != nil {
		return nil, errors.Join(err, parent.close())
	}
	return &archiveDestination{parent: parent, name: name, existing: existing}, nil
}

func (c *Client) extractArchive(ctx context.Context, archive *stagedContent, destination string, requiredMarkers ...string) (resultErr error) {
	return c.extractArchiveWithHooks(ctx, archive, destination, archiveHooks{}, requiredMarkers...)
}

func (c *Client) extractArchiveWithHooks(ctx context.Context, archive *stagedContent, destination string, hooks archiveHooks, requiredMarkers ...string) (resultErr error) {
	prepared, err := prepareArchiveDestination(destination)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, prepared.close())
	}()
	return c.extractArchiveInto(ctx, archive, prepared, hooks, requiredMarkers...)
}

func (c *Client) extractArchiveInto(ctx context.Context, archive *stagedContent, destination *archiveDestination, hooks archiveHooks, requiredMarkers ...string) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("extract remote archive: %w", err)
	}
	if archive == nil || destination == nil || destination.parent == nil {
		return unsafeRemoteFilesystem("verified remote archive or destination is unavailable")
	}
	opened, err := archive.openRead()
	if err != nil {
		return fmt.Errorf("open verified remote archive: %w", err)
	}
	archiveClosed := false
	defer func() {
		if !archiveClosed {
			resultErr = errors.Join(resultErr, classifyArchiveError(ctx, "close verified remote archive", opened.Close()))
		}
	}()
	info, err := opened.Stat()
	if err != nil {
		return classifyArchiveError(ctx, "inspect verified remote archive", err)
	}
	compressedSize := info.Size()
	if compressedSize <= 0 || compressedSize > maxRemoteContentSize {
		return unsafeRemoteFilesystem("verified remote archive size %d is outside 1..%d bytes", compressedSize, maxRemoteContentSize)
	}
	if hooks.beforeInspect != nil {
		hooks.beforeInspect()
	}
	manifest, err := scanArchive(ctx, opened, compressedSize)
	if err != nil {
		return err
	}
	if hooks.afterInspect != nil {
		hooks.afterInspect()
	}

	output, err := destination.parent.createOwned(".apm-archive-")
	if err != nil {
		return fmt.Errorf("create private remote archive output: %w", err)
	}
	files := make([]*stagedContent, 0)
	directories := make([]*ownedDirectory, 0)
	published := false
	defer func() {
		if published {
			for _, file := range files {
				file.name = ""
			}
			for index := len(directories) - 1; index >= 0; index-- {
				resultErr = errors.Join(resultErr, directories[index].release())
			}
			resultErr = errors.Join(resultErr, output.release())
			return
		}
		for index := len(files) - 1; index >= 0; index-- {
			resultErr = errors.Join(resultErr, files[index].remove())
		}
		for index := len(directories) - 1; index >= 0; index-- {
			resultErr = errors.Join(resultErr, directories[index].close())
		}
		resultErr = errors.Join(resultErr, output.close())
	}()

	directoryIndex, err := createArchiveDirectories(ctx, output, manifest, &directories)
	if err != nil {
		return err
	}
	if err := c.extractArchiveFiles(ctx, opened, compressedSize, manifest, directoryIndex, &files, hooks); err != nil {
		return err
	}
	closeArchiveErr := opened.Close()
	archiveClosed = true
	if closeArchiveErr != nil {
		return classifyArchiveError(ctx, "close verified remote archive", closeArchiveErr)
	}
	for _, marker := range requiredMarkers {
		if !validSecureEntryName(marker) {
			return unsafeRemoteFilesystem("invalid required remote archive marker %q", marker)
		}
		if err := output.directory.requireRegular(marker); err != nil {
			return err
		}
	}
	if err := archive.remove(); err != nil {
		return fmt.Errorf("remove verified remote archive before publication: %w", err)
	}
	if err := output.prepareForPublish(0o755); err != nil {
		return err
	}
	if hooks.beforePublish != nil {
		hooks.beforePublish()
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("publish remote archive: %w", err)
	}
	if destination.existing != nil {
		if err := destination.existing.removeIfStillEmpty(); err != nil {
			return err
		}
	}
	if hooks.afterDestinationRemove != nil {
		hooks.afterDestinationRemove()
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("publish remote archive: %w", err)
	}
	if err := output.publishNoReplace(destination.name); err != nil {
		return err
	}
	published = true
	return nil
}

func scanArchive(ctx context.Context, archive *os.File, compressedSize int64) (*archiveManifest, error) {
	manifest := &archiveManifest{nodes: make(map[string]archiveNode)}
	actualTotal := int64(0)
	err := walkArchive(ctx, archive, compressedSize, func(header *tar.Header, input io.Reader) error {
		node, err := validateArchiveHeader(header)
		if err != nil {
			return err
		}
		if _, duplicate := manifest.nodes[node.name]; duplicate {
			return unsafeRemoteFilesystem("remote archive path %q is duplicated after normalization", node.name)
		}
		if len(manifest.headers)+1 > maxArchiveNodes {
			return unsafeRemoteFilesystem("remote archive exceeds %d nodes", maxArchiveNodes)
		}
		if node.directory {
			manifest.headers = append(manifest.headers, node)
			manifest.nodes[node.name] = node
			return nil
		}
		if node.size > maxArchiveFileSize {
			return unsafeRemoteFilesystem("remote archive file %q exceeds %d bytes", node.name, maxArchiveFileSize)
		}
		prefix := newArchivePrefix()
		actual, copyErr := io.CopyN(prefix, input, node.size)
		if copyErr != nil || actual != node.size {
			if copyErr == nil {
				copyErr = io.ErrUnexpectedEOF
			}
			return classifyArchiveError(ctx, fmt.Sprintf("read remote archive file %q", node.name), copyErr)
		}
		if actual > maxArchiveTreeSize-actualTotal {
			return unsafeRemoteFilesystem("remote archive exceeds %d extracted bytes", maxArchiveTreeSize)
		}
		actualTotal += actual
		if exceedsArchiveRatio(actualTotal, compressedSize) {
			return unsafeRemoteFilesystem("remote archive extraction ratio exceeds %d", maxArchiveRatio)
		}
		if unsafeArchivePayload(prefix.Bytes()) {
			return unsafeRemoteFilesystem("remote archive file %q contains an archive or executable signature", node.name)
		}
		manifest.headers = append(manifest.headers, node)
		manifest.nodes[node.name] = node
		return nil
	})
	if err != nil {
		return nil, err
	}
	manifest.size = actualTotal
	if err := addImplicitArchiveDirectories(manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func validateArchiveHeader(header *tar.Header) (archiveNode, error) {
	if header == nil {
		return archiveNode{}, unsafeRemoteFilesystem("remote archive contains a nil header")
	}
	name, err := normalizeArchiveName(header.Name)
	if err != nil {
		return archiveNode{}, err
	}
	node := archiveNode{name: name, explicit: true}
	switch header.Typeflag {
	case tar.TypeDir:
		if header.Size != 0 {
			return archiveNode{}, unsafeRemoteFilesystem("remote archive directory %q has nonzero size", name)
		}
		node.directory = true
		node.mode = 0o755
	case tar.TypeReg, tar.TypeRegA:
		if header.Size < 0 {
			return archiveNode{}, unsafeRemoteFilesystem("remote archive file %q has negative size", name)
		}
		if hasArchiveExtension(name) {
			return archiveNode{}, unsafeRemoteFilesystem("remote archive file %q has a nested archive extension", name)
		}
		node.mode = 0o644 | fs.FileMode(header.Mode)&0o111
		node.size = header.Size
	default:
		return archiveNode{}, unsafeRemoteFilesystem("remote archive path %q has unsupported type %d", name, header.Typeflag)
	}
	return node, nil
}

func normalizeArchiveName(raw string) (string, error) {
	if raw == "" || strings.ContainsRune(raw, '\\') || path.IsAbs(raw) || hasDrivePrefix(raw) {
		return "", unsafeRemoteFilesystem("remote archive path %q is ambiguous or absolute", raw)
	}
	cleaned := path.Clean(raw)
	if cleaned == "" || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || path.IsAbs(cleaned) || hasDrivePrefix(cleaned) {
		return "", unsafeRemoteFilesystem("remote archive path %q escapes its root", raw)
	}
	if len([]byte(cleaned)) > maxArchivePathSize {
		return "", unsafeRemoteFilesystem("remote archive path %q exceeds %d bytes", cleaned, maxArchivePathSize)
	}
	if depth := len(strings.Split(cleaned, "/")); depth > maxArchiveDepth {
		return "", unsafeRemoteFilesystem("remote archive path %q exceeds depth %d", cleaned, maxArchiveDepth)
	}
	return cleaned, nil
}

func hasDrivePrefix(name string) bool {
	return len(name) >= 2 && ((name[0] >= 'a' && name[0] <= 'z') || (name[0] >= 'A' && name[0] <= 'Z')) && name[1] == ':'
}

func hasArchiveExtension(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range []string{".tar.gz", ".tgz", ".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".rar"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func addImplicitArchiveDirectories(manifest *archiveManifest) error {
	explicitNames := make([]string, 0, len(manifest.nodes))
	for name := range manifest.nodes {
		explicitNames = append(explicitNames, name)
	}
	for _, name := range explicitNames {
		parent := path.Dir(name)
		for parent != "." {
			if node, exists := manifest.nodes[parent]; exists {
				if !node.directory {
					return unsafeRemoteFilesystem("remote archive regular file %q is a parent path", parent)
				}
			} else {
				if len(manifest.nodes)+1 > maxArchiveNodes {
					return unsafeRemoteFilesystem("remote archive output exceeds %d nodes", maxArchiveNodes)
				}
				manifest.nodes[parent] = archiveNode{name: parent, directory: true, mode: 0o755}
			}
			parent = path.Dir(parent)
		}
	}
	return nil
}

func createArchiveDirectories(ctx context.Context, root *ownedDirectory, manifest *archiveManifest, created *[]*ownedDirectory) (map[string]*ownedDirectory, error) {
	index := map[string]*ownedDirectory{"": root}
	names := make([]string, 0)
	for name, node := range manifest.nodes {
		if node.directory {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(left, right int) bool {
		leftDepth := strings.Count(names[left], "/")
		rightDepth := strings.Count(names[right], "/")
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return names[left] < names[right]
	})
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("create remote archive directories: %w", err)
		}
		parentName := path.Dir(name)
		if parentName == "." {
			parentName = ""
		}
		parent := index[parentName]
		if parent == nil || parent.directory == nil {
			return nil, unsafeRemoteFilesystem("validated remote archive parent %q is unavailable", parentName)
		}
		owned, err := parent.directory.createOwnedNamed(path.Base(name), 0o755)
		if err != nil {
			return nil, err
		}
		*created = append(*created, owned)
		index[name] = owned
	}
	return index, nil
}

func (c *Client) extractArchiveFiles(
	ctx context.Context,
	archive *os.File,
	compressedSize int64,
	manifest *archiveManifest,
	directories map[string]*ownedDirectory,
	created *[]*stagedContent,
	hooks archiveHooks,
) error {
	headerIndex := 0
	actualTotal := int64(0)
	err := walkArchive(ctx, archive, compressedSize, func(header *tar.Header, input io.Reader) error {
		if headerIndex >= len(manifest.headers) {
			return unsafeRemoteFilesystem("remote archive changed between validation and extraction")
		}
		node, validationErr := validateArchiveHeader(header)
		if validationErr != nil {
			return validationErr
		}
		expected := manifest.headers[headerIndex]
		headerIndex++
		if node != expected {
			return unsafeRemoteFilesystem("remote archive header %q changed between validation and extraction", node.name)
		}
		if node.directory {
			return nil
		}
		parentName := path.Dir(node.name)
		if parentName == "." {
			parentName = ""
		}
		parent := directories[parentName]
		if parent == nil || parent.directory == nil {
			return unsafeRemoteFilesystem("validated remote archive file parent %q is unavailable", parentName)
		}
		staged, output, createErr := parent.directory.createNamedStaged(path.Base(node.name))
		if createErr != nil {
			return createErr
		}
		*created = append(*created, staged)
		actual, copyErr := io.CopyN(output, input, node.size)
		prepareErr := staged.prepareForClose(output, node.mode)
		closeErr := closeExtractedArchiveFile(output, hooks)
		if copyErr != nil || actual != node.size || prepareErr != nil || closeErr != nil {
			if copyErr == nil && actual != node.size {
				copyErr = io.ErrUnexpectedEOF
			}
			return classifyArchiveError(ctx, fmt.Sprintf("extract remote archive file %q", node.name), errors.Join(copyErr, prepareErr, closeErr))
		}
		if actual > maxArchiveTreeSize-actualTotal {
			return unsafeRemoteFilesystem("remote archive exceeds %d extracted bytes", maxArchiveTreeSize)
		}
		actualTotal += actual
		if exceedsArchiveRatio(actualTotal, compressedSize) {
			return unsafeRemoteFilesystem("remote archive extraction ratio exceeds %d", maxArchiveRatio)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if headerIndex != len(manifest.headers) || actualTotal != manifest.size {
		return unsafeRemoteFilesystem("remote archive changed between validation and extraction")
	}
	return nil
}

func closeExtractedArchiveFile(file *os.File, hooks archiveHooks) error {
	if hooks.closeFile != nil {
		return hooks.closeFile(file)
	}
	return file.Close()
}

func walkArchive(ctx context.Context, archive *os.File, compressedSize int64, visit func(*tar.Header, io.Reader) error) (resultErr error) {
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return classifyArchiveError(ctx, "rewind verified remote archive", err)
	}
	buffered := bufio.NewReader(&contextReader{ctx: ctx, reader: archive})
	gzipReader, err := gzip.NewReader(buffered)
	if err != nil {
		return classifyArchiveError(ctx, "open verified remote gzip content", err)
	}
	gzipReader.Multistream(false)
	closed := false
	defer func() {
		if !closed {
			resultErr = errors.Join(resultErr, classifyArchiveError(ctx, "close verified remote gzip content", gzipReader.Close()))
		}
	}()
	decompressed := newArchiveDecompressedReader(gzipReader, compressedSize)
	tarReader := tar.NewReader(decompressed)
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("read remote archive: %w", err)
		}
		header, nextErr := tarReader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return classifyArchiveError(ctx, "read remote tar header", nextErr)
		}
		if err := visit(header, tarReader); err != nil {
			return err
		}
	}
	var extra [1]byte
	count, readErr := decompressed.Read(extra[:])
	if count != 0 {
		return unsafeRemoteFilesystem("remote tar contains content after its terminator")
	}
	if !errors.Is(readErr, io.EOF) {
		return classifyArchiveError(ctx, "finish verified remote gzip content", readErr)
	}
	closeErr := gzipReader.Close()
	closed = true
	if closeErr != nil {
		return classifyArchiveError(ctx, "close verified remote gzip content", closeErr)
	}
	if _, trailingErr := buffered.ReadByte(); trailingErr == nil {
		return unsafeRemoteFilesystem("remote gzip contains trailing compressed content")
	} else if !errors.Is(trailingErr, io.EOF) {
		return classifyArchiveError(ctx, "check verified remote gzip boundary", trailingErr)
	}
	return nil
}

type archiveDecompressedReader struct {
	reader    io.Reader
	remaining int64
}

func newArchiveDecompressedReader(reader io.Reader, compressedSize int64) io.Reader {
	return &archiveDecompressedReader{reader: reader, remaining: archiveDecompressedBudget(compressedSize)}
}

func (reader *archiveDecompressedReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if reader.remaining > 0 {
		if int64(len(buffer)) > reader.remaining {
			buffer = buffer[:reader.remaining]
		}
		count, err := reader.reader.Read(buffer)
		reader.remaining -= int64(count)
		return count, err
	}
	var extra [1]byte
	count, err := reader.reader.Read(extra[:])
	if count > 0 {
		return 0, errArchiveDecompressedLimit
	}
	if err != nil {
		return 0, err
	}
	return 0, io.ErrNoProgress
}

// archiveDecompressedBudget 仅约束整个 gzip 解压流的压缩倍率，预算包含 tar 元数据、填充和结束块；
// regular-file 实际输出总量由归档 visitor 独立约束。
func archiveDecompressedBudget(compressedSize int64) int64 {
	if compressedSize <= 0 {
		return 0
	}
	if compressedSize > math.MaxInt64/maxArchiveRatio {
		return math.MaxInt64
	}
	return compressedSize * maxArchiveRatio
}

func classifyArchiveError(ctx context.Context, operation string, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s: %w", operation, ctxErr)
	}
	return fmt.Errorf("%s: %w", operation, errors.Join(cloud.ErrUnsafeContent, err))
}

func exceedsArchiveRatio(extracted, compressed int64) bool {
	if extracted <= 0 {
		return false
	}
	if compressed <= 0 {
		return true
	}
	if compressed > math.MaxInt64/maxArchiveRatio {
		return false
	}
	return extracted > compressed*maxArchiveRatio
}

type archivePrefix struct {
	contents []byte
}

func newArchivePrefix() *archivePrefix {
	return &archivePrefix{contents: make([]byte, 0, archivePrefixSize)}
}

func (prefix *archivePrefix) Write(contents []byte) (int, error) {
	remaining := archivePrefixSize - len(prefix.contents)
	if remaining > 0 {
		if len(contents) < remaining {
			remaining = len(contents)
		}
		prefix.contents = append(prefix.contents, contents[:remaining]...)
	}
	return len(contents), nil
}

func (prefix *archivePrefix) Bytes() []byte {
	return prefix.contents
}

func unsafeArchivePayload(contents []byte) bool {
	for _, signature := range [][]byte{
		{0x1f, 0x8b},
		{'P', 'K', 0x03, 0x04},
		{'P', 'K', 0x05, 0x06},
		{'P', 'K', 0x07, 0x08},
		{0x7f, 'E', 'L', 'F'},
		{'M', 'Z'},
		{0x00, 'a', 's', 'm'},
		{0xfe, 0xed, 0xfa, 0xce},
		{0xce, 0xfa, 0xed, 0xfe},
		{0xfe, 0xed, 0xfa, 0xcf},
		{0xcf, 0xfa, 0xed, 0xfe},
		{0xca, 0xfe, 0xba, 0xbe},
		{0xbe, 0xba, 0xfe, 0xca},
		{0xca, 0xfe, 0xba, 0xbf},
		{0xbf, 0xba, 0xfe, 0xca},
	} {
		if bytes.HasPrefix(contents, signature) {
			return true
		}
	}
	if len(contents) >= 262 && bytes.Equal(contents[257:262], []byte("ustar")) {
		return true
	}
	return false
}
