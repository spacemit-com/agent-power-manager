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
	"sort"
	"strings"
	"testing"

	"apm/internal/domain"
)

// agentYAMLWithAvatar 是带 metadata.avatar 的 v2 agent.yaml 文本；name 是外部身份。
const agentYAMLWithAvatar = "schema_version: 2\nagent:\n" +
	"  id: demo-agent\n" +
	"  name: demo-agent\n" +
	"  version: v1.0.0\n" +
	"  name_zh: 演示助手\n" +
	"  name_en: Demo Agent\n" +
	"  description: demo\n" +
	"  description_zh: 演示\n" +
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
	got, err := Validate(context.Background(), Request{Type: domain.PowerTypeExpert, Path: dir})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got.Name != "demo-agent" || got.Version != "v1.0.0" || got.Description != "demo" || got.FileCount != 2 || got.ContentSize <= 0 {
		t.Fatalf("Validate() = %+v", got)
	}
}

// TestValidateAgentWarnsWhenIDDiffersFromName 校验 agent.id 与 agent.name 取值不同时的 warning：
// Bibit 按 agent.id 定位已安装的 agents/<name> 目录，取值不同会让 Agent 在 Runtime 侧按 ID
// 找不到，但 APM 不比较二者，因此仍必须通过校验（ADR-033）。
func TestValidateAgentWarnsWhenIDDiffersFromName(t *testing.T) {
	dir := t.TempDir()
	writeAgentYAMLOnlyDir(t, dir, "schema_version: 2\nagent:\n"+
		"  id: demo-agent-internal\n"+
		"  name: demo-agent\n"+
		"  version: v1.0.0\n"+
		"  name_zh: 演示助手\n"+
		"  name_en: Demo Agent\n"+
		"  description: demo\n"+
		"  description_zh: 演示\n"+
		"  soul: help safely\n")
	got, err := Validate(context.Background(), Request{Type: domain.PowerTypeExpert, Path: dir})
	if err != nil {
		t.Fatalf("Validate() error = %v, want nil: id 与 name 不同取值不是校验错误", err)
	}
	if got.Name != "demo-agent" {
		t.Fatalf("Validate() name = %q, want demo-agent", got.Name)
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("Validate() warnings = %v, want exactly 1", got.Warnings)
	}
	for _, want := range []string{`agent.id "demo-agent-internal"`, `agent.name "demo-agent"`} {
		if !strings.Contains(got.Warnings[0], want) {
			t.Fatalf("warning %q does not mention %q", got.Warnings[0], want)
		}
	}
}

// TestValidateAgentDoesNotWarnWhenIDEqualsName 守护 id 与 name 相同取值不产生任何 warning。
func TestValidateAgentDoesNotWarnWhenIDEqualsName(t *testing.T) {
	dir := t.TempDir()
	writeValidAgentDir(t, dir)
	got, err := Validate(context.Background(), Request{Type: domain.PowerTypeExpert, Path: dir})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("Validate() warnings = %v, want none", got.Warnings)
	}
}

// TestPrepareExpertAcceptsIDNameMismatch 守护 submit 路径不受 ADR-034 影响：
// id 与 name 不同取值只影响 validate 的 warning，Prepare 仍必须成功。
func TestPrepareExpertAcceptsIDNameMismatch(t *testing.T) {
	root := t.TempDir()
	writeAgentYAMLOnlyDir(t, root, "schema_version: 2\nagent:\n"+
		"  id: demo-agent-internal\n"+
		"  name: demo-agent\n"+
		"  version: v1.0.0\n"+
		"  name_zh: 演示助手\n"+
		"  name_en: Demo Agent\n"+
		"  description: demo\n"+
		"  description_zh: 演示\n"+
		"  soul: help safely\n")
	got, err := Prepare(context.Background(), Request{Type: domain.PowerTypeExpert, Path: root, Message: "first"})
	if err != nil {
		t.Fatalf("Prepare() error = %v, want nil: id 与 name 不同取值不是提交错误", err)
	}
	if got.Name != "demo-agent" || got.Version != "v1.0.0" || got.ContentType != "application/gzip" {
		t.Fatalf("Prepare() = %+v", got)
	}
	assertTarGzFiles(t, got.Bytes, []string{"agent.yaml"})
}

func TestPrepareExpertProducesDeterministicGzip(t *testing.T) {
	root := t.TempDir()
	writeValidAgentDir(t, root)
	got, err := Prepare(context.Background(), Request{Type: domain.PowerTypeExpert, Path: root, Message: "first"})
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

func TestPrepareExpertIsDeterministicAcrossUnchangedDirectories(t *testing.T) {
	root := t.TempDir()
	writeValidAgentDir(t, root)
	request := Request{Type: domain.PowerTypeExpert, Path: root, Message: "first"}
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

func TestPrepareExpertNormalizesOutputModes(t *testing.T) {
	root := t.TempDir()
	writeValidAgentDir(t, root)
	got, err := Prepare(context.Background(), Request{Type: domain.PowerTypeExpert, Path: root, Message: "first"})
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
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeExpert, Path: path})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestPrepareExpertRejectsSingleYAMLFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	writeTestFile(t, path, validAgentYAML)
	_, err := Prepare(context.Background(), Request{Type: domain.PowerTypeExpert, Path: path, Message: "first"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Prepare() error = %v, want ErrInvalid", err)
	}
}

func TestPrepareExpertRejectsPrebuiltGzipFile(t *testing.T) {
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
	_, err = Prepare(context.Background(), Request{Type: domain.PowerTypeExpert, Path: prebuilt, Message: "first"})
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
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeExpert, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateAgentRejectsExtraRootEntry(t *testing.T) {
	dir := t.TempDir()
	writeValidAgentDir(t, dir)
	writeTestFile(t, filepath.Join(dir, "README.md"), "not allowed at root")
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeExpert, Path: dir})
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
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeExpert, Path: dir})
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
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeExpert, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestAgentSourcePermitsBinaryAssets(t *testing.T) {
	dir := t.TempDir()
	writeValidAgentDir(t, dir)
	writeTestFile(t, filepath.Join(dir, "assets", "data.bin"), string([]byte{0x00, 0x01, 0x02, 0xff}))
	got, err := Prepare(context.Background(), Request{Type: domain.PowerTypeExpert, Path: dir, Message: "first"})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	assertTarGzFiles(t, got.Bytes, []string{"agent.yaml", "assets/", "assets/avatar.png", "assets/data.bin"})
}

func TestValidateAgentRejectsNestedArchiveAsset(t *testing.T) {
	dir := t.TempDir()
	writeValidAgentDir(t, dir)
	writeTestFile(t, filepath.Join(dir, "assets", "payload.gz"), string([]byte{0x1f, 0x8b, 0x08, 0x00, 0x00}))
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeExpert, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestPrepareExpertRejectsOversizePackedOutput(t *testing.T) {
	dir := t.TempDir()
	writeValidAgentDir(t, dir)
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(filepath.Join(dir, "assets", fmt.Sprintf("noise-%d.bin", i)), randomBytes(10<<20), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := Prepare(context.Background(), Request{Type: domain.PowerTypeExpert, Path: dir, Message: "first"})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "packed content size") {
		t.Fatalf("Prepare() error = %v, want packed-size ErrInvalid", err)
	}
}

func TestPrepareExpertRejectsExpansionAboveLimit(t *testing.T) {
	dir := t.TempDir()
	writeValidAgentDir(t, dir)
	writeTestFile(t, filepath.Join(dir, "assets", "highly-compressible.txt"), strings.Repeat("a", 1<<20))
	_, err := Prepare(context.Background(), Request{Type: domain.PowerTypeExpert, Path: dir, Message: "first"})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "decompression ratio") {
		t.Fatalf("Prepare() error = %v, want decompression-ratio ErrInvalid", err)
	}
}

// --- Agent 携带独有子资源（ADR-046） ---

const expertSubSkillMD = "---\n" +
	"schema_version: 2\n" +
	"name: %NAME%\n" +
	"version: v1.0.0\n" +
	"description: Probe skill.\n" +
	"name_zh: 探针技能\n" +
	"name_en: Probe Skill\n" +
	"description_zh: 探针技能用途。\n" +
	"---\n" +
	"# Probe\n\nBody.\n"

const expertSubSkillCard = "---\n" +
	"schema_version: 2\n" +
	"---\n" +
	"# Skill Card: Probe Skill\n\n" +
	"## Capability\nDoes a thing.\n\n" +
	"## Use Cases\nProbing.\n\n" +
	"## Inputs and Outputs\nNone.\n\n" +
	"## Dependencies\nNone.\n\n" +
	"## Limitations\nNone.\n\n" +
	"## Maintainer\nOwner: Probe\nContact: probe@example.com\n"

const expertSubMCPJSON = `{"schema_version":2,"name":"%NAME%","name_zh":"探针MCP","name_en":"Probe MCP","version":"v1.0.0","description":"Probe MCP server.","description_zh":"探针MCP服务。","definition":{"transport":"stdio","command":"probe"}}`

const expertSubPluginJSON = `{"schema_version":2,"name":"%NAME%","version":"v1.0.0","description":"Probe plugin.","name_zh":"探针插件","name_en":"Probe Plugin","description_zh":"探针插件。","skills":["skills/probe"]}`

// writeExpertSubResourceDir 写入带独有 Skill、Plugin 与 MCP 子资源的 Agent 资源根。
func writeExpertSubResourceDir(t *testing.T, root string) {
	t.Helper()
	writeTestFile(t, filepath.Join(root, "agent.yaml"), agentYAMLWithAvatar)
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "avatar.png"), pngBytes(t, 128, 128), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "skills", "probe-skill", "SKILL.md"),
		strings.ReplaceAll(expertSubSkillMD, "%NAME%", "probe-skill"))
	writeTestFile(t, filepath.Join(root, "skills", "probe-skill", "skill-card.md"), expertSubSkillCard)
	writeTestFile(t, filepath.Join(root, "mcp", "probe-mcp.json"),
		strings.ReplaceAll(expertSubMCPJSON, "%NAME%", "probe-mcp"))
	writeTestFile(t, filepath.Join(root, "plugins", "probe-plugin", "apc-plugin.json"),
		strings.ReplaceAll(expertSubPluginJSON, "%NAME%", "probe-plugin"))
	writeTestFile(t, filepath.Join(root, "plugins", "probe-plugin", "skills", "probe", "SKILL.md"),
		strings.ReplaceAll(expertSubSkillMD, "%NAME%", "probe-inner"))
	writeTestFile(t, filepath.Join(root, "plugins", "probe-plugin", "skills", "probe", "skill-card.md"), expertSubSkillCard)
}

func TestValidateAgentAcceptsSubResources(t *testing.T) {
	root := t.TempDir()
	writeExpertSubResourceDir(t, root)

	validation, err := Validate(context.Background(), Request{Type: domain.PowerTypeExpert, Path: root})
	if err != nil {
		t.Fatalf("Validate() error = %v, want sub-resources accepted", err)
	}
	if validation.Name != "demo-agent" || validation.Version != "v1.0.0" {
		t.Fatalf("identity = %s@%s, want demo-agent@v1.0.0", validation.Name, validation.Version)
	}
	// 文件数必须计入子资源：agent.yaml + avatar + 2 Skill + 1 MCP + 1 清单 + 2 内嵌 Skill。
	if validation.FileCount != 8 {
		t.Fatalf("FileCount = %d, want 8 (sub-resources counted)", validation.FileCount)
	}
	if validation.ContentSize <= 0 {
		t.Fatalf("ContentSize = %d, want the sub-resource bytes counted", validation.ContentSize)
	}
}

func TestPrepareAgentPacksSubResourcesIntoOneArchive(t *testing.T) {
	root := t.TempDir()
	writeExpertSubResourceDir(t, root)

	content, err := Prepare(context.Background(), Request{
		Type: domain.PowerTypeExpert, Path: root, Message: "add exclusive sub-resources",
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if content.ContentType != "application/gzip" {
		t.Fatalf("ContentType = %q, want application/gzip", content.ContentType)
	}
	// 子资源仍打成一个 Expert 归档，且归档根不额外包裹 <name>/ 版本目录。
	headers := readTarGzHeaders(t, content.Bytes)
	for _, required := range []string{
		"agent.yaml",
		"assets/avatar.png",
		"skills/probe-skill/SKILL.md",
		"skills/probe-skill/skill-card.md",
		"mcp/probe-mcp.json",
		"plugins/probe-plugin/apc-plugin.json",
		"plugins/probe-plugin/skills/probe/SKILL.md",
		"plugins/probe-plugin/skills/probe/skill-card.md",
	} {
		if _, ok := headers[required]; !ok {
			t.Fatalf("archive is missing %q; entries = %v", required, sortedHeaderNames(headers))
		}
	}
	for name := range headers {
		if strings.HasPrefix(name, "demo-agent/") {
			t.Fatalf("archive entry %q is wrapped in a version directory", name)
		}
	}
}

func TestPrepareAgentSubResourcePackingIsDeterministic(t *testing.T) {
	root := t.TempDir()
	writeExpertSubResourceDir(t, root)

	first, err := Prepare(context.Background(), Request{Type: domain.PowerTypeExpert, Path: root, Message: "m"})
	if err != nil {
		t.Fatalf("Prepare() first error = %v", err)
	}
	second, err := Prepare(context.Background(), Request{Type: domain.PowerTypeExpert, Path: root, Message: "m"})
	if err != nil {
		t.Fatalf("Prepare() second error = %v", err)
	}
	if !bytes.Equal(first.Bytes, second.Bytes) {
		t.Fatal("two packing runs produced different bytes, want deterministic output")
	}
	if first.MD5 != second.MD5 {
		t.Fatalf("MD5 = %q vs %q, want identical", first.MD5, second.MD5)
	}
}

func TestValidateAgentRejectsIncompleteSubResources(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, root string)
		want   string
	}{
		{
			name: "skill missing SKILL.md",
			mutate: func(t *testing.T, root string) {
				if err := os.Remove(filepath.Join(root, "skills", "probe-skill", "SKILL.md")); err != nil {
					t.Fatal(err)
				}
			},
			want: "SKILL.md",
		},
		{
			name: "skill missing skill-card.md",
			mutate: func(t *testing.T, root string) {
				if err := os.Remove(filepath.Join(root, "skills", "probe-skill", "skill-card.md")); err != nil {
					t.Fatal(err)
				}
			},
			want: "skill-card.md",
		},
		{
			name: "mcp is not valid json",
			mutate: func(t *testing.T, root string) {
				writeTestFile(t, filepath.Join(root, "mcp", "probe-mcp.json"), "{")
			},
			want: "JSON",
		},
		{
			name: "plugin missing manifest",
			mutate: func(t *testing.T, root string) {
				if err := os.Remove(filepath.Join(root, "plugins", "probe-plugin", "apc-plugin.json")); err != nil {
					t.Fatal(err)
				}
			},
			want: "apc-plugin.json",
		},
		{
			name: "plugin undeclared skill",
			mutate: func(t *testing.T, root string) {
				writeTestFile(t, filepath.Join(root, "plugins", "probe-plugin", "skills", "extra", "SKILL.md"),
					strings.ReplaceAll(expertSubSkillMD, "%NAME%", "extra"))
			},
			want: "outside canonical schema",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeExpertSubResourceDir(t, root)
			test.mutate(t, root)

			_, err := Validate(context.Background(), Request{Type: domain.PowerTypeExpert, Path: root})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want a message naming %q", err, test.want)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Validate() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestValidateAgentStillRejectsUnknownRootEntry(t *testing.T) {
	// 放宽的是三个具名子资源目录，不是整个根闭集。
	root := t.TempDir()
	writeExpertSubResourceDir(t, root)
	writeTestFile(t, filepath.Join(root, "extra", "note.txt"), "hello")

	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeExpert, Path: root})
	if err == nil || !strings.Contains(err.Error(), "outside the root closed set") {
		t.Fatalf("Validate() error = %v, want the unknown root entry rejected", err)
	}
}

// sortedHeaderNames 返回排序后的归档条目名，仅用于失败信息。
func sortedHeaderNames(headers map[string]*tar.Header) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
