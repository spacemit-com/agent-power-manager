package remote

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"apm/internal/cloud"
)

type testTarEntry struct {
	name     string
	typeflag byte
	mode     int64
	size     int64
	body     []byte
	write    func(io.Writer) error
}

func TestExtractArchivePublishesValidatedTreeWithSanitizedModes(t *testing.T) {
	archive := stageTarGz(t,
		testTarEntry{name: "nested", typeflag: tar.TypeDir, mode: 0o777},
		testTarEntry{name: "nested/readme.md", mode: 0o666, body: []byte("safe text\n")},
		testTarEntry{name: "run.sh", mode: 0o6777, body: []byte("#!/bin/sh\nexit 0\n")},
	)
	destination := filepath.Join(trustedTestDir(t), "resource")
	if err := new(Client).extractArchive(context.Background(), archive, destination); err != nil {
		t.Fatal(err)
	}

	assertFileContentsAndMode(t, filepath.Join(destination, "nested", "readme.md"), "safe text\n", 0o644)
	assertFileContentsAndMode(t, filepath.Join(destination, "run.sh"), "#!/bin/sh\nexit 0\n", 0o755)
	if info, err := os.Stat(filepath.Join(destination, "nested")); err != nil || !info.IsDir() || info.Mode().Perm() != 0o755 {
		t.Fatalf("nested directory = %v, %v, want physical mode 0755", info, err)
	}
}

func TestExtractArchiveAcceptsImplicitDirectories(t *testing.T) {
	archive := stageTarGz(t, testTarEntry{name: "one/two/file.md", body: []byte("safe\n")})
	destination := filepath.Join(trustedTestDir(t), "resource")
	if err := new(Client).extractArchive(context.Background(), archive, destination); err != nil {
		t.Fatal(err)
	}
	assertFileContentsAndMode(t, filepath.Join(destination, "one", "two", "file.md"), "safe\n", 0o644)
}

func TestExtractArchiveRejectsUnsafeNamesWithoutPublishingDestination(t *testing.T) {
	tests := []struct {
		name    string
		archive func(*testing.T) *stagedContent
	}{
		{name: "absolute", archive: func(t *testing.T) *stagedContent {
			return stageTarGz(t, testTarEntry{name: "/outside", body: []byte("bad")})
		}},
		{name: "parent", archive: func(t *testing.T) *stagedContent {
			return stageTarGz(t, testTarEntry{name: "../outside", body: []byte("bad")})
		}},
		{name: "normalized parent", archive: func(t *testing.T) *stagedContent {
			return stageTarGz(t, testTarEntry{name: "safe/../../outside", body: []byte("bad")})
		}},
		{name: "empty", archive: stageTarGzWithEmptyName},
		{name: "dot", archive: func(t *testing.T) *stagedContent {
			return stageTarGz(t, testTarEntry{name: ".", typeflag: tar.TypeDir})
		}},
		{name: "drive", archive: func(t *testing.T) *stagedContent {
			return stageTarGz(t, testTarEntry{name: "C:/outside", body: []byte("bad")})
		}},
		{name: "backslash", archive: func(t *testing.T) *stagedContent {
			return stageTarGz(t, testTarEntry{name: `safe\outside`, body: []byte("bad")})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			destination := filepath.Join(trustedTestDir(t), "resource")
			err := new(Client).extractArchive(context.Background(), tt.archive(t), destination)
			assertUnsafeWithoutDestination(t, err, destination)
		})
	}
}

func TestExtractArchiveRejectsDuplicatesAndParentTypeConflicts(t *testing.T) {
	tests := []struct {
		name    string
		entries []testTarEntry
	}{
		{
			name: "duplicate after normalization",
			entries: []testTarEntry{
				{name: "same.md", body: []byte("first")},
				{name: "./same.md", body: []byte("second")},
			},
		},
		{
			name: "regular parent",
			entries: []testTarEntry{
				{name: "parent", body: []byte("file")},
				{name: "parent/child", body: []byte("bad")},
			},
		},
		{
			name: "regular parent declared after child",
			entries: []testTarEntry{
				{name: "parent/child", body: []byte("bad")},
				{name: "parent", body: []byte("file")},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			destination := filepath.Join(trustedTestDir(t), "resource")
			err := new(Client).extractArchive(context.Background(), stageTarGz(t, tt.entries...), destination)
			assertUnsafeWithoutDestination(t, err, destination)
		})
	}
}

func TestExtractArchiveRejectsNonRegularNodes(t *testing.T) {
	tests := []struct {
		name     string
		typeflag byte
	}{
		{name: "symlink", typeflag: tar.TypeSymlink},
		{name: "hardlink", typeflag: tar.TypeLink},
		{name: "character device", typeflag: tar.TypeChar},
		{name: "block device", typeflag: tar.TypeBlock},
		{name: "fifo", typeflag: tar.TypeFifo},
		{name: "socket-like unsupported type", typeflag: tar.TypeGNUSparse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			destination := filepath.Join(trustedTestDir(t), "resource")
			archive := stageTarGz(t, testTarEntry{name: "unsafe", typeflag: tt.typeflag})
			assertUnsafeWithoutDestination(t, new(Client).extractArchive(context.Background(), archive, destination), destination)
		})
	}
}

func TestExtractArchiveRejectsNestedArchivesAndExecutablesByContent(t *testing.T) {
	tarPayload := make([]byte, 512)
	copy(tarPayload[257:], []byte("ustar"))
	pePayload := make([]byte, 256)
	copy(pePayload, []byte("MZ"))
	binary.LittleEndian.PutUint32(pePayload[0x3c:], 0x80)
	copy(pePayload[0x80:], []byte{'P', 'E', 0, 0})
	latePEPayload := make([]byte, archivePrefixSize+4)
	copy(latePEPayload, []byte("MZ"))
	binary.LittleEndian.PutUint32(latePEPayload[0x3c:], archivePrefixSize)
	copy(latePEPayload[archivePrefixSize:], []byte{'P', 'E', 0, 0})
	tests := []struct {
		name string
		body []byte
	}{
		{name: "gzip", body: []byte{0x1f, 0x8b, 0x08, 0x00}},
		{name: "bzip2 disguised as markdown", body: []byte{'B', 'Z', 'h', '9'}},
		{name: "xz disguised as markdown", body: []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}},
		{name: "7z disguised as markdown", body: []byte{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}},
		{name: "RAR disguised as markdown", body: []byte{'R', 'a', 'r', '!', 0x1a, 0x07, 0x01, 0x00}},
		{name: "zip local", body: []byte{'P', 'K', 0x03, 0x04}},
		{name: "zip empty", body: []byte{'P', 'K', 0x05, 0x06}},
		{name: "tar", body: tarPayload},
		{name: "ELF", body: []byte{0x7f, 'E', 'L', 'F'}},
		{name: "PE", body: pePayload},
		{name: "MZ with invalid PE offset", body: []byte("MZ plain text")},
		{name: "PE signature at prefix boundary", body: latePEPayload},
		{name: "Mach-O 64 little endian", body: []byte{0xcf, 0xfa, 0xed, 0xfe}},
		{name: "Mach-O fat", body: []byte{0xca, 0xfe, 0xba, 0xbe}},
		{name: "Wasm", body: []byte{0x00, 'a', 's', 'm'}},
		{name: "invalid UTF-8", body: []byte("valid prefix\n\xff")},
		{name: "NUL in text", body: []byte("valid prefix\x00suffix")},
		{name: "forbidden control byte", body: []byte("valid prefix\x01suffix")},
		{name: "binary after old prefix boundary", body: append(bytes.Repeat([]byte("a"), archivePrefixSize), 0xff)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			destination := filepath.Join(trustedTestDir(t), "resource")
			archive := stageTarGz(t, testTarEntry{name: "payload.md", body: tt.body})
			assertUnsafeWithoutDestination(t, new(Client).extractArchive(context.Background(), archive, destination), destination)
		})
	}
}

func TestExtractArchiveAcceptsValidUTF8TextControls(t *testing.T) {
	body := []byte("标题\tvalue\r\nnext\fpage\n")
	archive := stageTarGz(t, testTarEntry{name: "notes.md", body: body})
	destination := filepath.Join(trustedTestDir(t), "resource")
	if err := new(Client).extractArchive(context.Background(), archive, destination); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(destination, "notes.md"))
	if err != nil || !bytes.Equal(contents, body) {
		t.Fatalf("published text = %q, %v, want %q", contents, err, body)
	}
}

func TestExtractArchiveRevalidatesPayloadDuringExtraction(t *testing.T) {
	archive := stageTarGz(t, testTarEntry{name: "safe.md", body: []byte("safe")})
	replacement := testTarGzBytes(t, testTarEntry{name: "safe.md", body: []byte{'B', 'Z', 'h', '9'}})
	destination := filepath.Join(trustedTestDir(t), "resource")
	err := new(Client).extractArchiveWithHooks(context.Background(), archive, destination, archiveHooks{
		afterInspect: func() {
			if err := os.WriteFile(filepath.Join(archive.directory.path, archive.name), replacement, 0o600); err != nil {
				t.Fatal(err)
			}
		},
	})
	assertUnsafeWithoutDestination(t, err, destination)
}

func TestExtractArchiveRejectsArchiveExtensionsCaseInsensitively(t *testing.T) {
	for _, name := range []string{"nested.GZ", "nested.Tar.Gz", "nested.TGZ", "nested.ZIP", "nested.TAR", "nested.BZ2", "nested.XZ", "nested.7Z", "nested.RAR"} {
		t.Run(name, func(t *testing.T) {
			destination := filepath.Join(trustedTestDir(t), "resource")
			archive := stageTarGz(t, testTarEntry{name: name, body: []byte("plain text")})
			assertUnsafeWithoutDestination(t, new(Client).extractArchive(context.Background(), archive, destination), destination)
		})
	}
}

func TestExtractArchiveRejectsCompressedSizeLimitBeforeGzipInspection(t *testing.T) {
	archive := stageSizedContent(t, maxRemoteContentSize+1)
	destination := filepath.Join(trustedTestDir(t), "resource")
	var inspected atomic.Bool
	err := new(Client).extractArchiveWithHooks(context.Background(), archive, destination, archiveHooks{
		beforeInspect: func() { inspected.Store(true) },
	})
	assertUnsafeWithoutDestination(t, err, destination)
	if inspected.Load() {
		t.Fatal("oversized compressed content was inspected as gzip")
	}
}

func TestExtractArchiveRejectsResourceLimits(t *testing.T) {
	t.Run("file over 10 MiB", func(t *testing.T) {
		destination := filepath.Join(trustedTestDir(t), "resource")
		archive := stageTarGz(t, testTarEntry{
			name: "large.md",
			size: maxArchiveFileSize + 1,
			write: func(output io.Writer) error {
				_, err := io.CopyN(output, zeroReader{}, maxArchiveFileSize+1)
				return err
			},
		})
		assertUnsafeWithoutDestination(t, new(Client).extractArchive(context.Background(), archive, destination), destination)
	})

	t.Run("tree over 100 MiB", func(t *testing.T) {
		entries := make([]testTarEntry, 0, 11)
		for index := range 10 {
			entries = append(entries, mixedEntropyEntry(fmt.Sprintf("file-%02d.md", index), maxArchiveFileSize, uint64(index+1)))
		}
		entries = append(entries, testTarEntry{name: "overflow.md", body: []byte("x")})
		archive := stageTarGz(t, entries...)
		assertArchiveFixtureRatio(t, archive, maxArchiveTreeSize+1)
		destination := filepath.Join(trustedTestDir(t), "resource")
		err := new(Client).extractArchive(context.Background(), archive, destination)
		assertUnsafeWithoutDestination(t, err, destination)
		if errors.Is(err, errArchiveDecompressedLimit) || !strings.Contains(err.Error(), "remote archive exceeds 104857600 extracted bytes") {
			t.Fatalf("extractArchive() error = %v, want extracted-tree limit rather than tar-stream framing limit", err)
		}
	})

	t.Run("more than 1000 nodes", func(t *testing.T) {
		entries := make([]testTarEntry, maxArchiveNodes+1)
		for index := range entries {
			entries[index] = testTarEntry{name: fmt.Sprintf("node-%04d.md", index)}
		}
		destination := filepath.Join(trustedTestDir(t), "resource")
		assertUnsafeWithoutDestination(t, new(Client).extractArchive(context.Background(), stageTarGz(t, entries...), destination), destination)
	})

	t.Run("depth over 16", func(t *testing.T) {
		name := strings.Repeat("d/", maxArchiveDepth) + "file.md"
		destination := filepath.Join(trustedTestDir(t), "resource")
		assertUnsafeWithoutDestination(t, new(Client).extractArchive(context.Background(), stageTarGz(t, testTarEntry{name: name}), destination), destination)
	})

	t.Run("path over 255 bytes", func(t *testing.T) {
		name := strings.Repeat("a", maxArchivePathSize-2) + ".md"
		destination := filepath.Join(trustedTestDir(t), "resource")
		assertUnsafeWithoutDestination(t, new(Client).extractArchive(context.Background(), stageTarGz(t, testTarEntry{name: name}), destination), destination)
	})

	t.Run("ratio over 100", func(t *testing.T) {
		destination := filepath.Join(trustedTestDir(t), "resource")
		archive := stageTarGz(t, testTarEntry{
			name: "compressible.md",
			size: 1 << 20,
			write: func(output io.Writer) error {
				_, err := io.CopyN(output, zeroReader{}, 1<<20)
				return err
			},
		})
		assertUnsafeWithoutDestination(t, new(Client).extractArchive(context.Background(), archive, destination), destination)
	})
}

func TestExtractArchiveAcceptsExactResourceLimits(t *testing.T) {
	t.Run("file exactly 10 MiB", func(t *testing.T) {
		archive := stageTarGz(t, mixedEntropyEntry("large.md", maxArchiveFileSize, 1))
		assertArchiveFixtureRatio(t, archive, maxArchiveFileSize)
		destination := filepath.Join(trustedTestDir(t), "resource")
		if err := new(Client).extractArchive(context.Background(), archive, destination); err != nil {
			t.Fatalf("extractArchive() error = %v, want exact 10 MiB file accepted", err)
		}
		if info, err := os.Stat(filepath.Join(destination, "large.md")); err != nil || info.Size() != maxArchiveFileSize {
			t.Fatalf("large.md = %v, %v, want exact %d-byte regular file", info, err, maxArchiveFileSize)
		}
	})

	t.Run("tree exactly 100 MiB", func(t *testing.T) {
		entries := make([]testTarEntry, 0, 10)
		for index := range 10 {
			entries = append(entries, mixedEntropyEntry(fmt.Sprintf("file-%02d.md", index), maxArchiveFileSize, uint64(index+1)))
		}
		archive := stageTarGz(t, entries...)
		compressedSize := stagedContentSize(t, archive)
		if compressedSize < 1<<20 || compressedSize > 20<<20 {
			t.Fatalf("compressed fixture size = %d, want 1..20 MiB", compressedSize)
		}
		assertArchiveFixtureRatio(t, archive, maxArchiveTreeSize)
		destination := filepath.Join(trustedTestDir(t), "resource")
		if err := new(Client).extractArchive(context.Background(), archive, destination); err != nil {
			t.Fatalf("extractArchive() error = %v, want exact 100 MiB output accepted", err)
		}
		var extractedSize int64
		for index := range 10 {
			info, err := os.Stat(filepath.Join(destination, fmt.Sprintf("file-%02d.md", index)))
			if err != nil {
				t.Fatal(err)
			}
			extractedSize += info.Size()
		}
		if extractedSize != maxArchiveTreeSize {
			t.Fatalf("extracted size = %d, want %d", extractedSize, maxArchiveTreeSize)
		}
	})

	t.Run("exactly 1000 nodes", func(t *testing.T) {
		entries := make([]testTarEntry, maxArchiveNodes)
		for index := range entries {
			entries[index] = testTarEntry{name: fmt.Sprintf("node-%04d.md", index)}
		}
		destination := filepath.Join(trustedTestDir(t), "resource")
		if err := new(Client).extractArchive(context.Background(), stageTarGz(t, entries...), destination); err != nil {
			t.Fatalf("extractArchive() error = %v, want exact 1000-node archive accepted", err)
		}
		if nodes, err := os.ReadDir(destination); err != nil || len(nodes) != maxArchiveNodes {
			t.Fatalf("output nodes = %d, %v, want %d", len(nodes), err, maxArchiveNodes)
		}
	})

	t.Run("depth exactly 16", func(t *testing.T) {
		name := strings.Repeat("d/", maxArchiveDepth-1) + "file.md"
		destination := filepath.Join(trustedTestDir(t), "resource")
		if err := new(Client).extractArchive(context.Background(), stageTarGz(t, testTarEntry{name: name, body: []byte("safe")}), destination); err != nil {
			t.Fatalf("extractArchive() error = %v, want exact depth-16 path accepted", err)
		}
		if _, err := os.Stat(filepath.Join(destination, filepath.FromSlash(name))); err != nil {
			t.Fatalf("depth-16 file: %v", err)
		}
	})

	t.Run("path exactly 255 bytes", func(t *testing.T) {
		name := strings.Repeat("a", maxArchivePathSize-3) + ".md"
		destination := filepath.Join(trustedTestDir(t), "resource")
		if err := new(Client).extractArchive(context.Background(), stageTarGz(t, testTarEntry{name: name, body: []byte("safe")}), destination); err != nil {
			t.Fatalf("extractArchive() error = %v, want exact 255-byte path accepted", err)
		}
		if _, err := os.Stat(filepath.Join(destination, name)); err != nil {
			t.Fatalf("255-byte path: %v", err)
		}
	})
}

func TestExtractArchiveRejectsPAXAndGNUExtendedMetadataBombs(t *testing.T) {
	const metadataSize = 512 << 10
	tests := []struct {
		name   string
		header tar.Header
	}{
		{
			name: "PAX records",
			header: tar.Header{
				Name:       "safe.md",
				Typeflag:   tar.TypeReg,
				Mode:       0o644,
				Size:       4,
				Format:     tar.FormatPAX,
				PAXRecords: map[string]string{"APM.metadata": strings.Repeat("x", metadataSize)},
			},
		},
		{
			name: "GNU long link",
			header: tar.Header{
				Name:     "safe.md",
				Linkname: strings.Repeat("x", metadataSize),
				Typeflag: tar.TypeReg,
				Mode:     0o644,
				Size:     4,
				Format:   tar.FormatGNU,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := stageTarGzWithHeader(t, &tt.header, []byte("safe"))
			opened, err := archive.openRead()
			if err != nil {
				t.Fatal(err)
			}
			info, statErr := opened.Stat()
			if err := errors.Join(statErr, opened.Close()); err != nil {
				t.Fatal(err)
			}
			if info.Size()*maxArchiveRatio >= metadataSize {
				t.Fatalf("compressed fixture size = %d, want metadata to exceed its 100x ratio budget", info.Size())
			}

			destination := filepath.Join(trustedTestDir(t), "resource")
			err = new(Client).extractArchive(context.Background(), archive, destination)
			assertUnsafeWithoutDestination(t, err, destination)
			if !errors.Is(err, errArchiveDecompressedLimit) {
				t.Fatalf("extractArchive() error = %v, want metadata rejected by decompressed limit", err)
			}
		})
	}
}

func TestExtractArchiveEnforcesDecompressedRatioAtExactByteBoundary(t *testing.T) {
	t.Run("exact budget is accepted", func(t *testing.T) {
		archive := stageArchiveBytes(t, exactRatioTarGzBytes(t, nil))
		destination := filepath.Join(trustedTestDir(t), "resource")
		if err := new(Client).extractArchive(context.Background(), archive, destination); err != nil {
			t.Fatalf("extractArchive() error = %v, want exact ratio budget accepted", err)
		}
		if info, err := os.Stat(filepath.Join(destination, "payload.md")); err != nil || info.Size() != 24_064 {
			t.Fatalf("payload = %v, %v, want 24064-byte regular file", info, err)
		}
	})

	t.Run("one decompressed byte over budget is rejected", func(t *testing.T) {
		archive := stageArchiveBytes(t, exactRatioTarGzBytes(t, []byte{'X'}))
		destination := filepath.Join(trustedTestDir(t), "resource")
		err := new(Client).extractArchive(context.Background(), archive, destination)
		assertUnsafeWithoutDestination(t, err, destination)
		if !errors.Is(err, errArchiveDecompressedLimit) {
			t.Fatalf("extractArchive() error = %v, want decompressed limit sentinel", err)
		}
	})
}

func TestExtractArchiveEnforcesDecompressedLimitAgainDuringExtraction(t *testing.T) {
	archive := stageTarGz(t, testTarEntry{name: "safe.md", body: []byte("safe")})
	replacement := tarGzHeaderBytes(t, &tar.Header{
		Name:       "safe.md",
		Typeflag:   tar.TypeReg,
		Mode:       0o644,
		Size:       4,
		Format:     tar.FormatPAX,
		PAXRecords: map[string]string{"APM.metadata": strings.Repeat("x", 512<<10)},
	}, []byte("safe"))
	destination := filepath.Join(trustedTestDir(t), "resource")
	err := new(Client).extractArchiveWithHooks(context.Background(), archive, destination, archiveHooks{
		afterInspect: func() {
			if err := os.WriteFile(filepath.Join(archive.directory.path, archive.name), replacement, 0o600); err != nil {
				t.Fatal(err)
			}
		},
	})
	assertUnsafeWithoutDestination(t, err, destination)
	if !errors.Is(err, errArchiveDecompressedLimit) {
		t.Fatalf("extractArchive() error = %v, want second-pass decompressed limit sentinel", err)
	}
}

func TestArchiveDecompressedReaderReportsOverflowWithoutOverreading(t *testing.T) {
	tests := []struct {
		name           string
		compressedSize int64
		contents       []byte
		wantContents   int
		wantRead       int
		wantLimitError bool
	}{
		{name: "exact ratio budget", compressedSize: 1, contents: make([]byte, 100), wantContents: 100, wantRead: 100},
		{name: "one over ratio budget", compressedSize: 1, contents: make([]byte, 101), wantContents: 100, wantRead: 101, wantLimitError: true},
		{name: "zero compressed size", compressedSize: 0, contents: []byte{0}, wantRead: 1, wantLimitError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &countingReader{reader: bytes.NewReader(tt.contents)}
			contents, err := io.ReadAll(newArchiveDecompressedReader(input, tt.compressedSize))
			if errors.Is(err, errArchiveDecompressedLimit) != tt.wantLimitError {
				t.Fatalf("ReadAll() error = %v, want limit error %t", err, tt.wantLimitError)
			}
			if len(contents) != tt.wantContents || input.read != tt.wantRead {
				t.Fatalf("ReadAll() returned %d bytes after reading %d, want %d returned and %d read", len(contents), input.read, tt.wantContents, tt.wantRead)
			}
		})
	}

	wantMaximumBudget := maxRemoteContentSize * maxArchiveRatio
	if got := archiveDecompressedBudget(maxRemoteContentSize); got != wantMaximumBudget {
		t.Fatalf("archiveDecompressedBudget(max compressed size) = %d, want %d", got, wantMaximumBudget)
	}
	if got := archiveDecompressedBudget(math.MaxInt64); got != math.MaxInt64 {
		t.Fatalf("archiveDecompressedBudget(MaxInt64) = %d, want overflow-safe saturation %d", got, int64(math.MaxInt64))
	}
	wantStreamSize := maxArchiveTreeSize + 1
	count, err := io.Copy(io.Discard, newArchiveDecompressedReader(io.LimitReader(zeroReader{}, wantStreamSize), maxRemoteContentSize))
	if err != nil || count != wantStreamSize {
		t.Fatalf("copy beyond extracted-tree limit = %d, %v, want exact %d-byte tar stream accepted", count, err, wantStreamSize)
	}
}

func TestExtractArchiveRejectsShortEntryAndTrailingTarContent(t *testing.T) {
	tests := []struct {
		name    string
		archive func(*testing.T) *stagedContent
	}{
		{name: "short regular entry", archive: stageShortTarGz},
		{name: "content after tar terminator", archive: stageTarGzWithTrailingContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			destination := filepath.Join(trustedTestDir(t), "resource")
			assertUnsafeWithoutDestination(t, new(Client).extractArchive(context.Background(), tt.archive(t), destination), destination)
		})
	}
}

func TestExtractArchiveRejectsConcatenatedAndTrailingGzipContent(t *testing.T) {
	valid := testTarGzBytes(t, testTarEntry{name: "safe.md", body: []byte("safe")})
	secondMember := testTarGzBytes(t, testTarEntry{name: "second.md", body: []byte("second")})
	tests := []struct {
		name     string
		contents []byte
	}{
		{name: "concatenated gzip member", contents: append(append([]byte(nil), valid...), secondMember...)},
		{name: "compressed trailing bytes", contents: append(append([]byte(nil), valid...), []byte("trailing")...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			destination := filepath.Join(trustedTestDir(t), "resource")
			assertUnsafeWithoutDestination(t, new(Client).extractArchive(context.Background(), stageArchiveBytes(t, tt.contents), destination), destination)
		})
	}
}

func TestExtractArchiveCleansPrivateOutputAfterExtractedFileCloseError(t *testing.T) {
	archive := stageTarGz(t, testTarEntry{name: "payload.md", body: []byte("safe")})
	parent := trustedTestDir(t)
	destination := filepath.Join(parent, "resource")
	injected := errors.New("injected extracted file close failure")
	err := new(Client).extractArchiveWithHooks(context.Background(), archive, destination, archiveHooks{
		closeFile: func(file *os.File) error {
			return errors.Join(file.Close(), injected)
		},
	})
	if !errors.Is(err, injected) || !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("extractArchive() error = %v, want injected close failure classified unsafe", err)
	}
	assertDirectoryEmpty(t, parent)
}

func TestExtractArchivePublishesIntoExistingEmptyPhysicalDirectory(t *testing.T) {
	archive := stageTarGz(t, testTarEntry{name: "payload.md", body: []byte("safe")})
	parent := trustedTestDir(t)
	destination := filepath.Join(parent, "resource")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := new(Client).extractArchive(context.Background(), archive, destination); err != nil {
		t.Fatal(err)
	}
	assertFileContentsAndMode(t, filepath.Join(destination, "payload.md"), "safe", 0o644)
}

func TestExtractArchiveNoReplacePublicationPreservesConcurrentDestination(t *testing.T) {
	archive := stageTarGz(t, testTarEntry{name: "payload.md", body: []byte("safe")})
	parent := trustedTestDir(t)
	destination := filepath.Join(parent, "resource")
	err := new(Client).extractArchiveWithHooks(context.Background(), archive, destination, archiveHooks{
		afterDestinationRemove: func() {
			if err := os.Mkdir(destination, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(destination, "competitor"), []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	})
	if !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("extractArchive() error = %v, want concurrent destination ErrUnsafeContent", err)
	}
	assertFileContentsAndMode(t, filepath.Join(destination, "competitor"), "keep", 0o600)
	assertOnlyEntry(t, parent, "resource")
}

func TestExtractArchiveRefusesSubstitutedExistingDirectoryWithoutDeletingIt(t *testing.T) {
	archive := stageTarGz(t, testTarEntry{name: "payload.md", body: []byte("safe")})
	parent := trustedTestDir(t)
	destination := filepath.Join(parent, "resource")
	moved := filepath.Join(parent, "original-empty")
	competitor := filepath.Join(parent, "competitor")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(competitor, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(competitor, "keep"), []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := new(Client).extractArchiveWithHooks(context.Background(), archive, destination, archiveHooks{
		beforePublish: func() {
			if err := os.Rename(destination, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(competitor, destination); err != nil {
				t.Fatal(err)
			}
		},
	})
	if !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("extractArchive() error = %v, want substituted destination ErrUnsafeContent", err)
	}
	if info, statErr := os.Lstat(destination); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("substituted destination = %v, %v, want preserved symlink", info, statErr)
	}
	assertFileContentsAndMode(t, filepath.Join(competitor, "keep"), "preserve", 0o600)
}

func stageTarGz(t *testing.T, entries ...testTarEntry) *stagedContent {
	t.Helper()
	return stageCompressedTar(t, func(tarOutput io.Writer) error { return writeTestTar(tarOutput, entries) })
}

func stageTarGzWithHeader(t *testing.T, header *tar.Header, body []byte) *stagedContent {
	t.Helper()
	return stageArchiveBytes(t, tarGzHeaderBytes(t, header, body))
}

func tarGzHeaderBytes(t *testing.T, header *tar.Header, body []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	writer := tar.NewWriter(gzipWriter)
	if err := writer.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(writer.Close(), gzipWriter.Close()); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func testTarGzBytes(t *testing.T, entries ...testTarEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	if err := writeTestTar(gzipWriter, entries); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func writeTestTar(output io.Writer, entries []testTarEntry) error {
	writer := tar.NewWriter(output)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		mode := entry.mode
		if mode == 0 {
			mode = 0o644
		}
		size := entry.size
		if entry.write == nil {
			size = int64(len(entry.body))
		}
		header := &tar.Header{Name: entry.name, Typeflag: typeflag, Mode: mode, Size: size}
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if entry.write != nil {
			if err := entry.write(writer); err != nil {
				return err
			}
		} else if _, err := writer.Write(entry.body); err != nil {
			return err
		}
	}
	return writer.Close()
}

func stageCompressedTar(t *testing.T, writeTar func(io.Writer) error) *stagedContent {
	t.Helper()
	parent := openTestPinnedDirectory(t, trustedTestDir(t))
	staged, file, err := parent.createStaged(".test-archive-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := staged.remove(); err != nil {
			t.Errorf("remove staged test archive: %v", err)
		}
	})
	gzipWriter := gzip.NewWriter(file)
	writeErr := writeTar(gzipWriter)
	gzipCloseErr := gzipWriter.Close()
	prepareErr := staged.prepareForClose(file, 0o600)
	closeErr := file.Close()
	if err := errors.Join(writeErr, gzipCloseErr, prepareErr, closeErr); err != nil {
		t.Fatal(err)
	}
	return staged
}

func stageArchiveBytes(t *testing.T, contents []byte) *stagedContent {
	t.Helper()
	parent := openTestPinnedDirectory(t, trustedTestDir(t))
	staged, file, err := parent.createStaged(".test-archive-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = staged.remove() })
	_, writeErr := file.Write(contents)
	prepareErr := staged.prepareForClose(file, 0o600)
	closeErr := file.Close()
	if err := errors.Join(writeErr, prepareErr, closeErr); err != nil {
		t.Fatal(err)
	}
	return staged
}

func exactRatioTarGzBytes(t *testing.T, trailing []byte) []byte {
	t.Helper()
	const (
		payloadSize     = 24_064
		decompressedLen = 25_600
		compressedLen   = decompressedLen / 100
	)
	var plain bytes.Buffer
	writer := tar.NewWriter(&plain)
	if err := writer.WriteHeader(&tar.Header{Name: "payload.md", Typeflag: tar.TypeReg, Mode: 0o644, Size: payloadSize}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(writer, repeatedByteReader('a'), payloadSize); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if plain.Len() != decompressedLen {
		t.Fatalf("plain tar size = %d, want %d", plain.Len(), decompressedLen)
	}
	plain.Write(trailing)

	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	if _, err := plain.WriteTo(gzipWriter); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return padGzipHeaderToSize(t, compressed.Bytes(), compressedLen)
}

func padGzipHeaderToSize(t *testing.T, contents []byte, size int) []byte {
	t.Helper()
	const gzipHeaderSize = 10
	extraFieldSize := size - len(contents) - 2
	if len(contents) < gzipHeaderSize || extraFieldSize < 0 || extraFieldSize > math.MaxUint16 {
		t.Fatalf("cannot pad %d-byte gzip stream to %d bytes", len(contents), size)
	}
	padded := make([]byte, size)
	copy(padded, contents[:gzipHeaderSize])
	padded[3] |= 0x04
	binary.LittleEndian.PutUint16(padded[gzipHeaderSize:], uint16(extraFieldSize))
	copy(padded[gzipHeaderSize+2+extraFieldSize:], contents[gzipHeaderSize:])
	return padded
}

func stageSizedContent(t *testing.T, size int64) *stagedContent {
	t.Helper()
	parent := openTestPinnedDirectory(t, trustedTestDir(t))
	staged, file, err := parent.createStaged(".test-archive-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = staged.remove() })
	if err := file.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(staged.prepareForClose(file, 0o600), file.Close()); err != nil {
		t.Fatal(err)
	}
	return staged
}

func stagedContentSize(t *testing.T, staged *stagedContent) int64 {
	t.Helper()
	file, err := staged.openRead()
	if err != nil {
		t.Fatal(err)
	}
	info, statErr := file.Stat()
	if err := errors.Join(statErr, file.Close()); err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func assertArchiveFixtureRatio(t *testing.T, staged *stagedContent, extractedSize int64) {
	t.Helper()
	compressedSize := stagedContentSize(t, staged)
	if extractedSize > compressedSize*maxArchiveRatio {
		t.Fatalf("fixture ratio = %d/%d, want no more than %dx", extractedSize, compressedSize, maxArchiveRatio)
	}
}

func stageTarGzWithEmptyName(t *testing.T) *stagedContent {
	t.Helper()
	var plain bytes.Buffer
	writer := tar.NewWriter(&plain)
	if err := writer.WriteHeader(&tar.Header{Name: "x", Typeflag: tar.TypeReg, Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	header := plain.Bytes()[:512]
	for index := range 100 {
		header[index] = 0
	}
	rewriteTarChecksum(header)
	return stageCompressedTar(t, func(output io.Writer) error {
		_, err := output.Write(plain.Bytes())
		return err
	})
}

func stageShortTarGz(t *testing.T) *stagedContent {
	t.Helper()
	return stageCompressedTar(t, func(output io.Writer) error {
		writer := tar.NewWriter(output)
		if err := writer.WriteHeader(&tar.Header{Name: "short.md", Typeflag: tar.TypeReg, Mode: 0o644, Size: 10}); err != nil {
			return err
		}
		_, _ = writer.Write([]byte("bad"))
		return nil
	})
}

func stageTarGzWithTrailingContent(t *testing.T) *stagedContent {
	t.Helper()
	var plain bytes.Buffer
	writer := tar.NewWriter(&plain)
	if err := writer.WriteHeader(&tar.Header{Name: "safe.md", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("safe")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	plain.Write(bytes.Repeat([]byte{'X'}, 512))
	return stageCompressedTar(t, func(output io.Writer) error {
		_, err := output.Write(plain.Bytes())
		return err
	})
}

func rewriteTarChecksum(header []byte) {
	for index := 148; index < 156; index++ {
		header[index] = ' '
	}
	var checksum int
	for _, value := range header {
		checksum += int(value)
	}
	copy(header[148:156], fmt.Sprintf("%06o\x00 ", checksum))
}

func mixedEntropyEntry(name string, size int64, seed uint64) testTarEntry {
	return testTarEntry{
		name: name,
		size: size,
		write: func(output io.Writer) error {
			random := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
			noise := make([]byte, 128<<10)
			for index := range noise {
				noise[index] = byte(0x20 + random.Uint64()%0x5f)
			}
			if _, err := output.Write(noise); err != nil {
				return err
			}
			_, err := io.CopyN(output, repeatedByteReader('a'), size-int64(len(noise)))
			return err
		},
	}
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}

type countingReader struct {
	reader io.Reader
	read   int
}

func (reader *countingReader) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	reader.read += count
	return count, err
}

func assertUnsafeWithoutDestination(t *testing.T, err error, destination string) {
	t.Helper()
	if !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("extractArchive() error = %v, want ErrUnsafeContent", err)
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination remains after unsafe archive: %v", statErr)
	}
	assertDirectoryEmpty(t, filepath.Dir(destination))
}

func assertFileContentsAndMode(t *testing.T, path, want string, mode os.FileMode) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != want {
		t.Fatalf("%s contents = %q, want %q", path, contents, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != mode {
		t.Fatalf("%s mode = %o, want %o", path, info.Mode().Perm(), mode)
	}
}

func assertOnlyEntry(t *testing.T, parent, name string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != name {
		t.Fatalf("directory %q entries = %v, want only %q", parent, entries, name)
	}
}
