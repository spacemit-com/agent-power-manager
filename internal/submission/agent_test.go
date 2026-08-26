package submission

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"apm/internal/domain"
)

// agentYAMLWithAvatar 是带 metadata.avatar 的 V0.5 agent.yaml 文本；name 是外部身份。
const agentYAMLWithAvatar = "schema_version: 1\nagent:\n" +
	"  id: demo-agent\n" +
	"  name: demo-agent\n" +
	"  version: v1.0.0\n" +
	"  description: demo\n" +
	"  soul: help safely\n" +
	"  metadata:\n" +
	"    avatar: assets/avatar.png\n"

// writeValidAgentDir 在 root 下写入合法 Agent 目录：agent.yaml 与生成的 assets/avatar.png。
func writeValidAgentDir(t *testing.T, root string) {
	t.Helper()
	writeTestFile(t, filepath.Join(root, "agent.yaml"), agentYAMLWithAvatar)
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "avatar.png"), pngBytes(t, 128, 128), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeAgentYAMLOnlyDir 在 root 下写入仅含 agent.yaml 的目录（不含 assets）。
func writeAgentYAMLOnlyDir(t *testing.T, root, agentYAML string) {
	t.Helper()
	writeTestFile(t, filepath.Join(root, "agent.yaml"), agentYAML)
}

// pngBytes 用 image.NewNRGBA + png.Encode 生成 width×height 的合法 PNG。
func pngBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// randomBytes 以固定种子生成不可压缩字节，用于验证打包大小上限。
func randomBytes(size int) []byte {
	rng := rand.New(rand.NewSource(42))
	data := make([]byte, size)
	if _, err := rng.Read(data); err != nil {
		panic(err)
	}
	return data
}

// assertTarGzFiles 按顺序断言 packed 归档的条目名与 want 完全一致（目录名以 / 结尾）。
func assertTarGzFiles(t *testing.T, packed []byte, want []string) {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var names []string
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
	}
	if len(names) != len(want) {
		t.Fatalf("tar entries = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entry %d = %q, want %q (all: %v)", i, names[i], want[i], names)
		}
	}
}

// readTarGzHeaders 读取归档全部条目的 tar.Header（含目录），按键返回。
func readTarGzHeaders(t *testing.T, packed []byte) map[string]*tar.Header {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	headers := make(map[string]*tar.Header)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		headers[header.Name] = header
	}
	return headers
}

func TestValidateAgentReturnsDirectoryMetadata(t *testing.T) {
	dir := t.TempDir()
	writeValidAgentDir(t, dir)
	got, err := Validate(context.Background(), Request{Type: domain.PowerTypeAgent, Path: dir})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got.Name != "demo-agent" || got.Version != "v1.0.0" || got.Description != "demo" || got.FileCount != 2 || got.ContentSize <= 0 {
		t.Fatalf("Validate() = %+v", got)
	}
}

func TestPrepareAgentProducesDeterministicGzip(t *testing.T) {
	root := t.TempDir()
	writeValidAgentDir(t, root)
	got, err := Prepare(context.Background(), Request{Type: domain.PowerTypeAgent, Path: root, Message: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "demo-agent" || got.ContentType != "application/gzip" {
		t.Fatalf("got %+v", got)
	}
	if got.Version != "v1.0.0" || got.Description != "demo" {
		t.Fatalf("identity = %+v", got)
	}
	assertTarGzFiles(t, got.Bytes, []string{"agent.yaml", "assets/", "assets/avatar.png"})
	verifyMD5(t, got.Bytes, got.MD5)
}

func TestPrepareAgentIsDeterministicAcrossUnchangedDirectories(t *testing.T) {
	root := t.TempDir()
	writeValidAgentDir(t, root)
	request := Request{Type: domain.PowerTypeAgent, Path: root, Message: "first"}
	first, err := Prepare(context.Background(), request)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	second, err := Prepare(context.Background(), request)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if !bytes.Equal(first.Bytes, second.Bytes) {
		t.Fatalf("Prepare() not deterministic")
	}
	if first.MD5 != second.MD5 {
		t.Fatalf("MD5 differs: %q vs %q", first.MD5, second.MD5)
	}
}

func TestPrepareAgentNormalizesOutputModes(t *testing.T) {
	root := t.TempDir()
	writeValidAgentDir(t, root)
	got, err := Prepare(context.Background(), Request{Type: domain.PowerTypeAgent, Path: root, Message: "first"})
	if err != nil {
		t.Fatal(err)
	}
	headers := readTarGzHeaders(t, got.Bytes)
	if mode := headers["agent.yaml"].Mode; mode != 0o644 {
		t.Fatalf("agent.yaml mode = %o, want 0644", mode)
	}
	if mode := headers["assets/avatar.png"].Mode; mode != 0o644 {
		t.Fatalf("avatar mode = %o, want 0644", mode)
	}
	if mode := headers["assets/"].Mode; mode != 0o755 {
		t.Fatalf("assets/ mode = %o, want 0755", mode)
	}
}

func TestValidateAgentRejectsSingleYAMLFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	writeTestFile(t, path, validAgentYAML)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeAgent, Path: path})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestPrepareAgentRejectsSingleYAMLFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	writeTestFile(t, path, validAgentYAML)
	_, err := Prepare(context.Background(), Request{Type: domain.PowerTypeAgent, Path: path, Message: "first"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Prepare() error = %v, want ErrInvalid", err)
	}
}

func TestPrepareAgentRejectsPrebuiltGzipFile(t *testing.T) {
	root := t.TempDir()
	writeValidAgentDir(t, root)
	prebuilt := filepath.Join(t.TempDir(), "demo-agent-v1.0.0.tar.gz")
	packed, err := BuildTarGz(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prebuilt, packed, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Prepare(context.Background(), Request{Type: domain.PowerTypeAgent, Path: prebuilt, Message: "first"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Prepare() error = %v, want ErrInvalid", err)
	}
}

func TestValidateAgentRejectsSymlinkInsideTree(t *testing.T) {
	dir := t.TempDir()
	writeValidAgentDir(t, dir)
	if err := os.Symlink("agent.yaml", filepath.Join(dir, "assets", "alias")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeAgent, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateAgentRejectsExtraRootEntry(t *testing.T) {
	dir := t.TempDir()
	writeValidAgentDir(t, dir)
	writeTestFile(t, filepath.Join(dir, "README.md"), "not allowed at root")
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeAgent, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateAgentRejectsHardLink(t *testing.T) {
	dir := t.TempDir()
	writeValidAgentDir(t, dir)
	link := filepath.Join(dir, "assets", "agent-alias.yaml")
	if err := os.Link(filepath.Join(dir, "agent.yaml"), link); err != nil {
		t.Skipf("hard link unsupported: %v", err)
	}
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeAgent, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateAgentRejectsExecutableFile(t *testing.T) {
	dir := t.TempDir()
	writeValidAgentDir(t, dir)
	if err := os.Chmod(filepath.Join(dir, "agent.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeAgent, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestAgentSourcePermitsBinaryAssets(t *testing.T) {
	dir := t.TempDir()
	writeValidAgentDir(t, dir)
	writeTestFile(t, filepath.Join(dir, "assets", "data.bin"), string([]byte{0x00, 0x01, 0x02, 0xff}))
	got, err := Prepare(context.Background(), Request{Type: domain.PowerTypeAgent, Path: dir, Message: "first"})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	assertTarGzFiles(t, got.Bytes, []string{"agent.yaml", "assets/", "assets/avatar.png", "assets/data.bin"})
}

func TestValidateAgentRejectsNestedArchiveAsset(t *testing.T) {
	dir := t.TempDir()
	writeValidAgentDir(t, dir)
	writeTestFile(t, filepath.Join(dir, "assets", "payload.gz"), string([]byte{0x1f, 0x8b, 0x08, 0x00, 0x00}))
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeAgent, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestPrepareAgentRejectsOversizePackedOutput(t *testing.T) {
	dir := t.TempDir()
	writeValidAgentDir(t, dir)
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(filepath.Join(dir, "assets", fmt.Sprintf("noise-%d.bin", i)), randomBytes(10<<20), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := Prepare(context.Background(), Request{Type: domain.PowerTypeAgent, Path: dir, Message: "first"})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "packed content size") {
		t.Fatalf("Prepare() error = %v, want packed-size ErrInvalid", err)
	}
}

func TestPrepareAgentRejectsExpansionAboveLimit(t *testing.T) {
	dir := t.TempDir()
	writeValidAgentDir(t, dir)
	writeTestFile(t, filepath.Join(dir, "assets", "highly-compressible.txt"), strings.Repeat("a", 1<<20))
	_, err := Prepare(context.Background(), Request{Type: domain.PowerTypeAgent, Path: dir, Message: "first"})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "decompression ratio") {
		t.Fatalf("Prepare() error = %v, want decompression-ratio ErrInvalid", err)
	}
}
