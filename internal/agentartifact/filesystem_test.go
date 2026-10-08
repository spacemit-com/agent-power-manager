package agentartifact_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"apm/internal/agentartifact"
	"apm/internal/domain"
)

// writeAgentPackage 在 root 下写入合法 Agent 包：agent.yaml + assets/avatar.png，
// 并追加 extraFiles（path 用 / 分隔）与 extraDirs。
func writeAgentPackage(t *testing.T, root string, extraFiles map[string][]byte, extraDirs []string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agent.yaml"), avatarAgentYAML("assets/avatar.png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "avatar.png"), pngBytes(t, 128, 128), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range extraDirs {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range extraFiles {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestValidateDirectoryAcceptsPackage 验证合法物理目录通过并返回正确快照。
func TestValidateDirectoryAcceptsPackage(t *testing.T) {
	root := t.TempDir()
	writeAgentPackage(t, root, map[string][]byte{
		"assets/skills.md": []byte("hello"),
		"assets/data.bin":  {0x00, 0x01, 0x02, 0xff},
	}, nil)
	snapshot, err := agentartifact.ValidateDirectory(context.Background(), root, nil)
	if err != nil {
		t.Fatalf("ValidateDirectory() error = %v", err)
	}
	if snapshot.Config.Agent.Name != "demo-agent" || snapshot.Config.Agent.Version != "v1.0.0" {
		t.Fatalf("ValidateDirectory() config = %+v", snapshot.Config)
	}
	if snapshot.FileCount != 4 {
		t.Fatalf("FileCount = %d, want 4", snapshot.FileCount)
	}
	var wantSize int64
	paths := make(map[string]bool, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		paths[entry.Path] = true
		if !entry.Directory {
			wantSize += int64(len(entry.Data))
		}
	}
	for _, want := range []string{"agent.yaml", "assets", "assets/avatar.png", "assets/skills.md", "assets/data.bin"} {
		if !paths[want] {
			t.Fatalf("ValidateDirectory() missing entry %q: %v", want, snapshot.Entries)
		}
	}
	if snapshot.ContentSize != wantSize {
		t.Fatalf("ContentSize = %d, want %d", snapshot.ContentSize, wantSize)
	}
}

// TestValidateDirectoryAcceptsPackageWithoutAssets 验证无 assets/ 的最小目录也通过。
func TestValidateDirectoryAcceptsPackageWithoutAssets(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "agent.yaml"),
		minimalAgentYAML("bibit-internal-42", "demo-agent", "v1.0.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := agentartifact.ValidateDirectory(context.Background(), root, nil)
	if err != nil {
		t.Fatalf("ValidateDirectory() error = %v", err)
	}
	if snapshot.FileCount != 1 || snapshot.Entries[0].Path != "agent.yaml" {
		t.Fatalf("ValidateDirectory() snapshot = %+v", snapshot)
	}
}

// TestValidateDirectoryAllowsEmptyAssets 验证空 assets/ 目录允许存在。
func TestValidateDirectoryAllowsEmptyAssets(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "agent.yaml"),
		minimalAgentYAML("bibit-internal-42", "demo-agent", "v1.0.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := agentartifact.ValidateDirectory(context.Background(), root, nil); err != nil {
		t.Fatalf("ValidateDirectory() error = %v", err)
	}
}

// TestValidateDirectoryRejectsNonDirectoryRoot 验证非目录根被物理树检查拒绝。
func TestValidateDirectoryRejectsNonDirectoryRoot(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "payload")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := agentartifact.ValidateDirectory(context.Background(), filePath, nil); err == nil {
		t.Fatal("ValidateDirectory() error = nil, want rejection")
	}
}

// TestValidateDirectoryRejectsSymlink 验证树内符号链接被物理树检查拒绝。
func TestValidateDirectoryRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	writeAgentPackage(t, root, nil, nil)
	if err := os.Symlink(filepath.Join(root, "agent.yaml"), filepath.Join(root, "assets", "escape.yaml")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := agentartifact.ValidateDirectory(context.Background(), root, nil); err == nil {
		t.Fatal("ValidateDirectory() error = nil, want symlink rejection")
	}
}

// TestValidateDirectoryRejectsHardLink 验证硬链接被物理树检查拒绝。
func TestValidateDirectoryRejectsHardLink(t *testing.T) {
	root := t.TempDir()
	writeAgentPackage(t, root, nil, nil)
	if err := os.Link(filepath.Join(root, "agent.yaml"), filepath.Join(root, "assets", "linked.yaml")); err != nil {
		t.Skipf("hard link unavailable: %v", err)
	}
	if _, err := agentartifact.ValidateDirectory(context.Background(), root, nil); err == nil {
		t.Fatal("ValidateDirectory() error = nil, want hard-link rejection")
	}
}

// TestValidateDirectoryEnforcesFileSizeLimit 验证单文件 10 MiB 边界。
func TestValidateDirectoryEnforcesFileSizeLimit(t *testing.T) {
	overRoot := t.TempDir()
	writeAgentPackage(t, overRoot, map[string][]byte{
		"assets/big.bin": bytes.Repeat([]byte{0xab}, 10<<20+1),
	}, nil)
	if _, err := agentartifact.ValidateDirectory(context.Background(), overRoot, nil); err == nil {
		t.Fatal("ValidateDirectory() error = nil, want per-file size rejection")
	}

	boundaryRoot := t.TempDir()
	writeAgentPackage(t, boundaryRoot, map[string][]byte{
		"assets/big.bin": bytes.Repeat([]byte{0xab}, 10<<20),
	}, nil)
	if _, err := agentartifact.ValidateDirectory(context.Background(), boundaryRoot, nil); err != nil {
		t.Fatalf("ValidateDirectory() boundary 10 MiB error = %v", err)
	}
}

// TestValidateDirectoryEnforcesTotalSizeLimit 验证全部文件总量 100 MiB 上限。
func TestValidateDirectoryEnforcesTotalSizeLimit(t *testing.T) {
	root := t.TempDir()
	tenMiB := bytes.Repeat([]byte{0xcd}, 10<<20)
	extra := make(map[string][]byte)
	for index := 0; index < 10; index++ {
		extra[fmt.Sprintf("assets/f%02d.bin", index)] = tenMiB
	}
	writeAgentPackage(t, root, extra, nil)
	if _, err := agentartifact.ValidateDirectory(context.Background(), root, nil); err == nil {
		t.Fatal("ValidateDirectory() error = nil, want total size rejection")
	}
}

// TestValidateDirectoryEnforcesNodeLimit 验证节点数 1000 上限（agent.yaml + assets + 999 文件 = 1001）。
func TestValidateDirectoryEnforcesNodeLimit(t *testing.T) {
	root := t.TempDir()
	extra := make(map[string][]byte)
	for index := 0; index < 999; index++ {
		extra[fmt.Sprintf("assets/n%03d.txt", index)] = []byte("x")
	}
	writeAgentPackage(t, root, extra, nil)
	if _, err := agentartifact.ValidateDirectory(context.Background(), root, nil); err == nil {
		t.Fatal("ValidateDirectory() error = nil, want node-count rejection")
	}
}

// TestValidateDirectoryEnforcesDepthLimit 验证目录深度 16 上限（17 段路径被拒绝）。
func TestValidateDirectoryEnforcesDepthLimit(t *testing.T) {
	root := t.TempDir()
	writeAgentPackage(t, root, nil, nil)
	deep := "assets"
	for index := 0; index < 16; index++ {
		deep += fmt.Sprintf("/d%02d", index)
	}
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(deep)), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := agentartifact.ValidateDirectory(context.Background(), root, nil); err == nil {
		t.Fatal("ValidateDirectory() error = nil, want depth rejection")
	}
}

// TestValidateDirectoryEnforcesPathLengthLimit 验证规范相对路径 255 字节上限。
func TestValidateDirectoryEnforcesPathLengthLimit(t *testing.T) {
	root := t.TempDir()
	long := strings.Repeat("x", 150)
	writeAgentPackage(t, root, nil, []string{filepath.Join("assets", long)})
	if err := os.WriteFile(filepath.Join(root, "assets", long, long+".bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := agentartifact.ValidateDirectory(context.Background(), root, nil); err == nil {
		t.Fatal("ValidateDirectory() error = nil, want path-length rejection")
	}
}

// TestValidateDirectoryHonorsContextCancellation 验证已取消的 ctx 中止遍历。
func TestValidateDirectoryHonorsContextCancellation(t *testing.T) {
	root := t.TempDir()
	writeAgentPackage(t, root, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := agentartifact.ValidateDirectory(ctx, root, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ValidateDirectory() error = %v, want context.Canceled", err)
	}
}

// TestValidateDirectoryRejectsExecutableFiles 验证物理目录中的可执行文件被拒绝。
func TestValidateDirectoryRejectsExecutableFiles(t *testing.T) {
	root := t.TempDir()
	writeAgentPackage(t, root, map[string][]byte{"assets/tool.sh": []byte("#!/bin/sh\n")}, nil)
	if err := os.Chmod(filepath.Join(root, "assets", "tool.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := agentartifact.ValidateDirectory(context.Background(), root, nil)
	if !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("ValidateDirectory() error = %v, want ErrInvalidAgent", err)
	}
}

// TestValidateDirectoryPowerRefIdentity 验证目录校验同样执行 PowerRef 身份比较。
func TestValidateDirectoryPowerRefIdentity(t *testing.T) {
	root := t.TempDir()
	writeAgentPackage(t, root, nil, nil)
	matching := domain.PowerRef{Type: domain.PowerTypeExpert, Name: "demo-agent", Version: "v1.0.0"}
	if _, err := agentartifact.ValidateDirectory(context.Background(), root, &matching); err != nil {
		t.Fatalf("ValidateDirectory() matching ref error = %v", err)
	}
	wrong := domain.PowerRef{Type: domain.PowerTypeExpert, Name: "demo-agent", Version: "v9.9.9"}
	if _, err := agentartifact.ValidateDirectory(context.Background(), root, &wrong); !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("ValidateDirectory() error = %v, want ErrInvalidAgent", err)
	}
}
