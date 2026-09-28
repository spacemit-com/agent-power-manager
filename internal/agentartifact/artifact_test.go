package agentartifact_test

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"strings"
	"testing"

	"apm/internal/agentartifact"
	"apm/internal/domain"
)

// minimalAgentYAML 生成满足 V0.5 最小必填字段的 agent.yaml 文本。
func minimalAgentYAML(id, name, version string) []byte {
	return []byte("schema_version: 1\nagent:\n" +
		"  id: " + id + "\n" +
		"  name: " + name + "\n" +
		"  version: " + version + "\n" +
		"  description: demo\n" +
		"  soul: help safely\n")
}

// avatarAgentYAML 生成带 metadata.avatar 的 agent.yaml 文本。
func avatarAgentYAML(avatar string) []byte {
	return []byte("schema_version: 1\nagent:\n" +
		"  id: bibit-internal-42\n" +
		"  name: demo-agent\n" +
		"  version: v1.0.0\n" +
		"  description: demo\n" +
		"  soul: help safely\n" +
		"  metadata:\n" +
		"    avatar: " + avatar + "\n")
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

// noisePNGBytes 生成随机像素的 PNG（压缩率极低，用于超过 1 MiB 的用例）。
func noisePNGBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(42))
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(rng.Intn(256)), G: uint8(rng.Intn(256)),
				B: uint8(rng.Intn(256)), A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func fileEntry(path string, data []byte) agentartifact.Entry {
	return agentartifact.Entry{Path: path, Directory: false, Mode: 0o644, Data: data}
}

func dirEntry(path string) agentartifact.Entry {
	return agentartifact.Entry{Path: path, Directory: true, Mode: 0o755}
}

// TestValidateMinimalPackage 验证最小包（仅 agent.yaml）通过，快照字段正确。
func TestValidateMinimalPackage(t *testing.T) {
	yaml := minimalAgentYAML("bibit-internal-42", "demo-agent", "v1.0.0")
	snapshot, err := agentartifact.Validate([]agentartifact.Entry{
		fileEntry("agent.yaml", yaml),
	}, nil)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if snapshot.Config.Agent.ID != "bibit-internal-42" ||
		snapshot.Config.Agent.Name != "demo-agent" ||
		snapshot.Config.Agent.Version != "v1.0.0" ||
		snapshot.Config.Agent.Description != "demo" ||
		snapshot.Config.Agent.Soul != "help safely" {
		t.Fatalf("Validate() config = %+v", snapshot.Config)
	}
	if snapshot.FileCount != 1 || snapshot.ContentSize != int64(len(yaml)) || len(snapshot.Entries) != 1 {
		t.Fatalf("Validate() snapshot = %+v", snapshot)
	}
	if string(snapshot.Entries[0].Data) != string(yaml) {
		t.Fatalf("snapshot entry data mismatch")
	}
}

// TestValidateAcceptsAssetsWithoutAvatar 验证缺省 avatar 时不要求 assets/，且允许普通文件与二进制 asset。
func TestValidateAcceptsAssetsWithoutAvatar(t *testing.T) {
	yaml := minimalAgentYAML("bibit-internal-42", "demo-agent", "v1.0.0")
	entries := []agentartifact.Entry{
		fileEntry("agent.yaml", yaml),
		dirEntry("assets"),
		fileEntry("assets/skills.md", []byte("hello")),
		fileEntry("assets/data.bin", []byte{0x00, 0x01, 0x02}),
		dirEntry("assets/nested"),
	}
	snapshot, err := agentartifact.Validate(entries, nil)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if snapshot.FileCount != 3 {
		t.Fatalf("FileCount = %d, want 3", snapshot.FileCount)
	}
	if snapshot.ContentSize != int64(len(yaml)+len("hello")+3) {
		t.Fatalf("ContentSize = %d", snapshot.ContentSize)
	}
}

// TestValidateAcceptsAvatarBounds 验证 128 与 1024 边界尺寸的方形 PNG 均通过。
func TestValidateAcceptsAvatarBounds(t *testing.T) {
	for _, side := range []int{128, 1024} {
		t.Run(fmt.Sprintf("side-%d", side), func(t *testing.T) {
			entries := []agentartifact.Entry{
				fileEntry("agent.yaml", avatarAgentYAML("assets/avatar.png")),
				dirEntry("assets"),
				fileEntry("assets/avatar.png", pngBytes(t, side, side)),
			}
			if _, err := agentartifact.Validate(entries, nil); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

// TestValidateRejectsBlankAvatar 验证空字符串 avatar 被拒绝（关闭 Task 1 minor finding）。
func TestValidateRejectsBlankAvatar(t *testing.T) {
	for name, raw := range map[string][]byte{
		"empty": avatarAgentYAML(`""`),
		"blank": avatarAgentYAML(`"   "`),
	} {
		t.Run(name, func(t *testing.T) {
			entries := []agentartifact.Entry{
				fileEntry("agent.yaml", raw),
				dirEntry("assets"),
				fileEntry("assets/avatar.png", pngBytes(t, 128, 128)),
			}
			if _, err := agentartifact.Validate(entries, nil); !errors.Is(err, domain.ErrInvalidAgent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
			}
		})
	}
}

// TestValidateRejectsNonCanonicalAvatarPath 验证 avatar 路径逃逸、绝对路径、反斜杠、非 assets 前缀与非 .png 后缀。
func TestValidateRejectsNonCanonicalAvatarPath(t *testing.T) {
	cases := map[string]string{
		"outside assets":        "avatar.png",
		"nested outside assets": "icons/avatar.png",
		"absolute":              "/assets/avatar.png",
		"current dir":           "./assets/avatar.png",
		"parent traversal":      "assets/../avatar.png",
		"double slash":          "assets//avatar.png",
		"backslash":             `assets\avatar.png`,
		"trailing slash":        "assets/avatar.png/",
		"wrong extension":       "assets/avatar.jpeg",
		"uppercase extension":   "assets/avatar.PNG",
	}
	for name, avatar := range cases {
		t.Run(name, func(t *testing.T) {
			entries := []agentartifact.Entry{
				fileEntry("agent.yaml", avatarAgentYAML(avatar)),
				dirEntry("assets"),
				fileEntry("assets/avatar.png", pngBytes(t, 128, 128)),
			}
			if _, err := agentartifact.Validate(entries, nil); !errors.Is(err, domain.ErrInvalidAgent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
			}
		})
	}
}

// TestValidateRejectsMissingAvatarTarget 验证 avatar 必须解析到快照内的普通文件。
func TestValidateRejectsMissingAvatarTarget(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		entries := []agentartifact.Entry{
			fileEntry("agent.yaml", avatarAgentYAML("assets/missing.png")),
			dirEntry("assets"),
		}
		if _, err := agentartifact.Validate(entries, nil); !errors.Is(err, domain.ErrInvalidAgent) {
			t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
		}
	})
	t.Run("directory target", func(t *testing.T) {
		entries := []agentartifact.Entry{
			fileEntry("agent.yaml", avatarAgentYAML("assets/sub/icon.png")),
			dirEntry("assets"),
			dirEntry("assets/sub/icon.png"),
		}
		if _, err := agentartifact.Validate(entries, nil); !errors.Is(err, domain.ErrInvalidAgent) {
			t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
		}
	})
}

// TestValidateRejectsInvalidAvatarFile 验证空文件与伪造 PNG（坏签名、签名有效但不可解码）。
func TestValidateRejectsInvalidAvatarFile(t *testing.T) {
	pngSignature := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	cases := map[string][]byte{
		"empty file":        {},
		"bad signature":     []byte("definitely not a png"),
		"signature no IHDR": append(append([]byte{}, pngSignature...), 0x01, 0x02, 0x03),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			entries := []agentartifact.Entry{
				fileEntry("agent.yaml", avatarAgentYAML("assets/avatar.png")),
				dirEntry("assets"),
				fileEntry("assets/avatar.png", data),
			}
			if _, err := agentartifact.Validate(entries, nil); !errors.Is(err, domain.ErrInvalidAgent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
			}
		})
	}
}

// TestValidateRejectsAvatarDimensionViolations 验证非正方形与 127/1025 越界尺寸。
func TestValidateRejectsAvatarDimensionViolations(t *testing.T) {
	cases := map[string][2]int{
		"non-square 128x64":   {128, 64},
		"non-square 1024x512": {1024, 512},
		"side 127":            {127, 127},
		"side 1025":           {1025, 1025},
	}
	for name, size := range cases {
		t.Run(name, func(t *testing.T) {
			entries := []agentartifact.Entry{
				fileEntry("agent.yaml", avatarAgentYAML("assets/avatar.png")),
				dirEntry("assets"),
				fileEntry("assets/avatar.png", pngBytes(t, size[0], size[1])),
			}
			if _, err := agentartifact.Validate(entries, nil); !errors.Is(err, domain.ErrInvalidAgent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
			}
		})
	}
}

// TestValidateRejectsOversizedAvatar 验证超过 1 MiB 的 PNG 被大小检查拒绝。
func TestValidateRejectsOversizedAvatar(t *testing.T) {
	entries := []agentartifact.Entry{
		fileEntry("agent.yaml", avatarAgentYAML("assets/avatar.png")),
		dirEntry("assets"),
		fileEntry("assets/avatar.png", noisePNGBytes(t, 1024, 1024)),
	}
	_, err := agentartifact.Validate(entries, nil)
	if !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Validate() error = %v, want size rejection", err)
	}
}

// TestValidateRejectsRootClosedSetViolations 验证根闭集：仅 agent.yaml 与可选 assets/ 目录。
func TestValidateRejectsRootClosedSetViolations(t *testing.T) {
	yaml := minimalAgentYAML("bibit-internal-42", "demo-agent", "v1.0.0")
	cases := map[string][]agentartifact.Entry{
		"extra root file": {
			fileEntry("agent.yaml", yaml),
			fileEntry("readme.md", []byte("x")),
		},
		"extra root directory": {
			fileEntry("agent.yaml", yaml),
			dirEntry("docs"),
		},
		"assets as file": {
			fileEntry("agent.yaml", yaml),
			fileEntry("assets", []byte("x")),
		},
		"agent.yaml as directory": {
			dirEntry("agent.yaml"),
		},
		"missing agent.yaml": {
			dirEntry("assets"),
		},
		"empty snapshot": {},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := agentartifact.Validate(entries, nil); !errors.Is(err, domain.ErrInvalidAgent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
			}
		})
	}
}

// TestValidateRejectsMultiSegmentPathsOutsideClosedSet 验证根闭集同样适用于多段路径：
// 首段既非 agent.yaml 也非 assets 的条目（即使没有父目录条目）必须被拒绝；
// assets/ 下的深层文件（无需父目录条目）仍然通过。
func TestValidateRejectsMultiSegmentPathsOutsideClosedSet(t *testing.T) {
	yaml := minimalAgentYAML("bibit-internal-42", "demo-agent", "v1.0.0")
	cases := map[string][]agentartifact.Entry{
		"docs/readme.md without parent entry": {
			fileEntry("agent.yaml", yaml),
			fileEntry("docs/readme.md", []byte("x")),
		},
		"assets2/x without parent entry": {
			fileEntry("agent.yaml", yaml),
			fileEntry("assets2/x", []byte("x")),
		},
		"deep outside assets": {
			fileEntry("agent.yaml", yaml),
			fileEntry("docs/sub/readme.md", []byte("x")),
		},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := agentartifact.Validate(entries, nil); !errors.Is(err, domain.ErrInvalidAgent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
			}
		})
	}
	t.Run("assets subpath without parent entry", func(t *testing.T) {
		entries := []agentartifact.Entry{
			fileEntry("agent.yaml", yaml),
			fileEntry("assets/deep/nested/file.txt", []byte("x")),
		}
		if _, err := agentartifact.Validate(entries, nil); err != nil {
			t.Fatalf("Validate() error = %v, want pass", err)
		}
	})
}

// TestValidateRejectsExecutableFiles 验证可执行普通文件被拒绝。
func TestValidateRejectsExecutableFiles(t *testing.T) {
	yaml := minimalAgentYAML("bibit-internal-42", "demo-agent", "v1.0.0")
	cases := map[string][]agentartifact.Entry{
		"executable agent.yaml": {
			{Path: "agent.yaml", Directory: false, Mode: 0o755, Data: yaml},
		},
		"executable asset": {
			fileEntry("agent.yaml", yaml),
			dirEntry("assets"),
			{Path: "assets/tool.sh", Directory: false, Mode: 0o754, Data: []byte("echo")},
		},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := agentartifact.Validate(entries, nil); !errors.Is(err, domain.ErrInvalidAgent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
			}
		})
	}
}

// TestValidateRejectsNestedArchives 验证按扩展名与按已知归档签名拒绝嵌套归档。
func TestValidateRejectsNestedArchives(t *testing.T) {
	yaml := minimalAgentYAML("bibit-internal-42", "demo-agent", "v1.0.0")
	for _, name := range []string{
		"assets/bad.zip", "assets/bad.tar", "assets/bad.tgz", "assets/bad.tar.gz",
		"assets/bad.gz", "assets/bad.bz2", "assets/bad.xz", "assets/bad.7z",
	} {
		t.Run("extension "+name, func(t *testing.T) {
			entries := []agentartifact.Entry{
				fileEntry("agent.yaml", yaml),
				dirEntry("assets"),
				fileEntry(name, []byte("payload")),
			}
			if _, err := agentartifact.Validate(entries, nil); !errors.Is(err, domain.ErrInvalidAgent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
			}
		})
	}

	signatures := map[string][]byte{
		"zip":       {'P', 'K', 0x03, 0x04},
		"empty zip": {'P', 'K', 0x05, 0x06},
		"gzip":      {0x1f, 0x8b},
		"bzip2":     {'B', 'Z', 'h'},
		"xz":        {0xfd, '7', 'z', 'X', 'Z', 0x00},
		"7z":        {'7', 'z', 0xbc, 0xaf, 0x27, 0x1c},
	}
	for name, signature := range signatures {
		t.Run("signature "+name, func(t *testing.T) {
			entries := []agentartifact.Entry{
				fileEntry("agent.yaml", yaml),
				dirEntry("assets"),
				fileEntry("assets/blob.bin", signature),
			}
			if _, err := agentartifact.Validate(entries, nil); !errors.Is(err, domain.ErrInvalidAgent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
			}
		})
	}

	t.Run("signature tar", func(t *testing.T) {
		data := make([]byte, 262)
		copy(data[257:], []byte("ustar"))
		entries := []agentartifact.Entry{
			fileEntry("agent.yaml", yaml),
			dirEntry("assets"),
			fileEntry("assets/blob.dat", data),
		}
		if _, err := agentartifact.Validate(entries, nil); !errors.Is(err, domain.ErrInvalidAgent) {
			t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
		}
	})
}

// TestValidateRejectsNonCanonicalAndDuplicatePaths 验证歧义路径与重复规范化路径被拒绝。
func TestValidateRejectsNonCanonicalAndDuplicatePaths(t *testing.T) {
	yaml := minimalAgentYAML("bibit-internal-42", "demo-agent", "v1.0.0")
	cases := map[string][]agentartifact.Entry{
		"absolute": {
			{Path: "/agent.yaml", Directory: false, Mode: 0o644, Data: yaml},
		},
		"current dir": {
			{Path: "./agent.yaml", Directory: false, Mode: 0o644, Data: yaml},
		},
		"traversal": {
			{Path: "assets/../../agent.yaml", Directory: false, Mode: 0o644, Data: yaml},
		},
		"double slash": {
			{Path: "assets//agent.yaml", Directory: false, Mode: 0o644, Data: yaml},
			dirEntry("assets"),
			fileEntry("assets/agent.yaml", []byte("x")),
		},
		"backslash": {
			{Path: `assets\agent.yaml`, Directory: false, Mode: 0o644, Data: yaml},
		},
		"trailing slash": {
			{Path: "assets/", Directory: true, Mode: 0o755},
		},
		"duplicate": {
			fileEntry("agent.yaml", yaml),
			dirEntry("assets"),
			fileEntry("assets/avatar.png", []byte("a")),
			fileEntry("assets/avatar.png", []byte("b")),
		},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := agentartifact.Validate(entries, nil); !errors.Is(err, domain.ErrInvalidAgent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
			}
		})
	}
}

// TestValidatePowerRef 验证 ref 为 nil 时跳过身份比较，非 nil 时必须完全一致。
func TestValidatePowerRef(t *testing.T) {
	entries := []agentartifact.Entry{
		fileEntry("agent.yaml", minimalAgentYAML("bibit-internal-42", "demo-agent", "v1.0.0")),
	}
	matching := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "demo-agent", Version: "v1.0.0"}
	if _, err := agentartifact.Validate(entries, &matching); err != nil {
		t.Fatalf("Validate() with matching ref error = %v", err)
	}
	for name, wrong := range map[string]domain.PowerRef{
		"wrong type":    {Type: domain.PowerTypeSkill, Name: "demo-agent", Version: "v1.0.0"},
		"wrong name":    {Type: domain.PowerTypeAgent, Name: "other-agent", Version: "v1.0.0"},
		"wrong version": {Type: domain.PowerTypeAgent, Name: "demo-agent", Version: "v1.0.1"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := agentartifact.Validate(entries, &wrong); !errors.Is(err, domain.ErrInvalidAgent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
			}
		})
	}
}

// TestValidateRejectsInvalidAgentYAML 验证 agent.yaml 语义失败统一包裹 ErrInvalidAgent。
func TestValidateRejectsInvalidAgentYAML(t *testing.T) {
	for name, raw := range map[string]string{
		"unsupported schema": "schema_version: 3\nagent: {id: bibit, name: demo-agent, version: v1.0.0, description: demo, soul: help safely}\n",
		"legacy zh_name":     "schema_version: 2\nagent: {id: bibit, name: demo-agent, version: v1.0.0, description: demo, name_zh: 演示, description_zh: 演示, zh_name: 旧键, soul: help safely}\n",
		"missing name":       "schema_version: 1\nagent: {id: bibit, version: v1.0.0, description: demo, soul: help safely}\n",
		"multiple docs":      "schema_version: 1\nagent: {id: bibit, name: demo-agent, version: v1.0.0, description: demo, soul: help safely}\n---\n{}\n",
		"not agent yaml":     "not: valid\n",
		"empty":              "",
	} {
		t.Run(name, func(t *testing.T) {
			entries := []agentartifact.Entry{fileEntry("agent.yaml", []byte(raw))}
			if _, err := agentartifact.Validate(entries, nil); !errors.Is(err, domain.ErrInvalidAgent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
			}
		})
	}
}

// TestValidatePreservesEntryOrder 验证校验器不重排条目，快照保持输入顺序。
func TestValidatePreservesEntryOrder(t *testing.T) {
	entries := []agentartifact.Entry{
		dirEntry("assets"),
		fileEntry("assets/avatar.png", pngBytes(t, 128, 128)),
		fileEntry("agent.yaml", avatarAgentYAML("assets/avatar.png")),
	}
	snapshot, err := agentartifact.Validate(entries, nil)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for index, want := range []string{"assets", "assets/avatar.png", "agent.yaml"} {
		if snapshot.Entries[index].Path != want {
			t.Fatalf("Validate() reordered entries: %+v", snapshot.Entries)
		}
	}
}

// TestValidateDefensivelyCopiesEntryData 验证返回的 Entry.Data 不与调用方缓冲别名。
func TestValidateDefensivelyCopiesEntryData(t *testing.T) {
	yaml := minimalAgentYAML("bibit-internal-42", "demo-agent", "v1.0.0")
	avatar := pngBytes(t, 128, 128)
	pristineYAML := bytes.Clone(yaml)
	pristineAvatar := bytes.Clone(avatar)
	entries := []agentartifact.Entry{
		fileEntry("agent.yaml", yaml),
		dirEntry("assets"),
		fileEntry("assets/avatar.png", avatar),
	}
	snapshot, err := agentartifact.Validate(entries, nil)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	entries[0].Data[0] = 'X'
	entries[2].Data[0] = 'X'
	if !bytes.Equal(snapshot.Entries[0].Data, pristineYAML) {
		t.Fatalf("snapshot agent.yaml aliases caller buffer")
	}
	if !bytes.Equal(snapshot.Entries[2].Data, pristineAvatar) {
		t.Fatalf("snapshot avatar aliases caller buffer")
	}
	snapshot.Entries[2].Data[0] = 'Y'
	if got := avatar[0]; got != 'X' {
		t.Fatalf("caller avatar buffer aliases snapshot: got %q", got)
	}
}
