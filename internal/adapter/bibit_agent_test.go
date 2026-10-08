package adapter

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"apm/internal/domain"
	"apm/internal/paths"
)

func newBibitAgentFixture(t *testing.T, createRoot bool) (root, staging string) {
	t.Helper()
	home := t.TempDir()
	root = filepath.Join(home, ".bianbu-agent")
	t.Setenv("APM_HOME", filepath.Join(home, "apm-home"))
	t.Setenv("BIANBU_AGENT_HOME", root)
	if createRoot {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	staging, err = paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	return root, staging
}

// testAgentAvatarPNG 是 128x128 有效 PNG，满足 avatar 校验（签名、正方形与尺寸）。
var testAgentAvatarPNG = func() []byte {
	var output bytes.Buffer
	if err := png.Encode(&output, image.NewNRGBA(image.Rect(0, 0, 128, 128))); err != nil {
		panic("encode test agent avatar PNG: " + err.Error())
	}
	return output.Bytes()
}()

// agentFixtureYAML 返回与 ref 身份一致、avatar 指向 assets/avatar.png 的有效 Agent fixture。
func agentFixtureYAML(ref domain.PowerRef) string {
	return "schema_version: 1\nagent: {id: " + ref.Name + ", name: " + ref.Name + ", version: " + ref.Version + ", description: demo, soul: demo, metadata: {avatar: assets/avatar.png}}\n"
}

// writeAgentDirectory 在 base/agents/<name> 下写入 agent.yaml 与 assets/avatar.png，
// 构成一个完整且有效的 Agent 目录。
func writeAgentDirectory(t *testing.T, base, name string, raw []byte) {
	t.Helper()
	dir := filepath.Join(base, "agents", name)
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "avatar.png"), testAgentAvatarPNG, 0o644); err != nil {
		t.Fatal(err)
	}
}

// walkAgentTree 收集 root 下所有文件的规范相对路径到正文的映射。
func walkAgentTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	tree := make(map[string][]byte)
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if relative == "." || entry.IsDir() {
			return nil
		}
		body, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		tree[filepath.ToSlash(relative)] = body
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func newBibitAgentAdapterForTest() *bibitAgentAdapter {
	return newBibitAgentAdapter(agentProfile{id: domain.AgentBibit, homeEnv: "BIANBU_AGENT_HOME", defaultRoot: ".bianbu-agent"})
}

func TestBibitAgentAdapterTargetPathIsDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BIANBU_AGENT_HOME", root)
	a := newBibitAgentAdapterForTest()
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeExpert, Version: "v1.0.0"}
	target, err := a.TargetPath(ref)
	if err != nil || target != filepath.Join(root, "agents", "bianbu-agent-default") {
		t.Fatalf("TargetPath() = %q, %v", target, err)
	}
}

func TestBibitAgentAdapterInstallsCompleteDirectory(t *testing.T) {
	root, staging := newBibitAgentFixture(t, true)
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v1.0.0"}
	raw := []byte(agentFixtureYAML(ref))
	writeAgentDirectory(t, staging, ref.Name, raw)

	a := newBibitAgentAdapterForTest()
	result, err := a.Install(ref, staging)
	if err != nil || !result.Published {
		t.Fatalf("Install() = %+v, %v", result, err)
	}
	target := filepath.Join(root, "agents", ref.Name)
	want := map[string][]byte{
		"agent.yaml":        raw,
		"assets/avatar.png": testAgentAvatarPNG,
	}
	if got := walkAgentTree(t, target); !reflect.DeepEqual(got, want) {
		t.Fatalf("installed tree = %v, want %v", got, want)
	}
	if _, err := os.Lstat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging not cleaned: %v", err)
	}
}

func TestBibitAgentAdapterInstallsNormalizedModes(t *testing.T) {
	root, staging := newBibitAgentFixture(t, true)
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v1.0.0"}
	dir := filepath.Join(staging, "agents", ref.Name)
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(agentFixtureYAML(ref)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "avatar.png"), testAgentAvatarPNG, 0o600); err != nil {
		t.Fatal(err)
	}

	a := newBibitAgentAdapterForTest()
	result, err := a.Install(ref, staging)
	if err != nil || !result.Published {
		t.Fatalf("Install() = %+v, %v", result, err)
	}
	target := filepath.Join(root, "agents", ref.Name)
	for path, want := range map[string]os.FileMode{
		"agent.yaml":        0o644,
		"assets":            0o755,
		"assets/avatar.png": 0o644,
	} {
		info, statErr := os.Stat(filepath.Join(target, filepath.FromSlash(path)))
		if statErr != nil || info.Mode().Perm() != want {
			t.Fatalf("%s mode = %v, %v; want %o", path, info, statErr, want)
		}
	}
}

func TestBibitAgentAdapterValidatesIdentityByName(t *testing.T) {
	_, staging := newBibitAgentFixture(t, true)
	raw := []byte("schema_version: 1\nagent: {id: bibit-internal-42, name: demo, version: v1.0.0, description: demo, soul: demo, metadata: {avatar: assets/avatar.png}}\n")
	writeAgentDirectory(t, staging, "demo", raw)
	a := newBibitAgentAdapterForTest()
	directory := filepath.Join(staging, "agents", "demo")

	if err := a.ValidateArtifact(domain.PowerRef{Type: domain.PowerTypeExpert, Name: "demo", Version: "v1.0.0"}, directory); err != nil {
		t.Fatalf("ValidateArtifact(name) error = %v", err)
	}
	if err := a.ValidateArtifact(domain.PowerRef{Type: domain.PowerTypeExpert, Name: "bibit-internal-42", Version: "v1.0.0"}, directory); !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("ValidateArtifact(id-as-name) error = %v, want ErrInvalidAgent", err)
	}
}

func TestBibitAgentAdapterInvalidAvatarPublishesNothing(t *testing.T) {
	root, staging := newBibitAgentFixture(t, true)
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v1.0.0"}
	dir := filepath.Join(staging, "agents", ref.Name)
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(agentFixtureYAML(ref)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "avatar.png"), []byte("not a png"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := newBibitAgentAdapterForTest()
	if result, err := a.Install(ref, staging); err == nil || result.Published {
		t.Fatalf("Install() = %+v, %v, want validation failure", result, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "agents", ref.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target published: %v", err)
	}
	if _, err := os.Lstat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging not cleaned: %v", err)
	}
}

func TestBibitAgentAdapterExistingTargetReturnsConflict(t *testing.T) {
	root, staging := newBibitAgentFixture(t, true)
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v1.0.0"}
	writeAgentDirectory(t, staging, ref.Name, []byte(agentFixtureYAML(ref)))
	if err := os.MkdirAll(filepath.Join(root, "agents", ref.Name), 0o755); err != nil {
		t.Fatal(err)
	}

	a := newBibitAgentAdapterForTest()
	result, err := a.Install(ref, staging)
	if !errors.Is(err, ErrTargetConflict) || result.Published {
		t.Fatalf("Install() = %+v, %v, want ErrTargetConflict", result, err)
	}
	if _, err := os.Lstat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging not cleaned: %v", err)
	}
}

func TestBibitAgentAdapterCopyFailureCleansPrivateAndStaging(t *testing.T) {
	root, staging := newBibitAgentFixture(t, true)
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v1.0.0"}
	writeAgentDirectory(t, staging, ref.Name, []byte(agentFixtureYAML(ref)))

	a := newBibitAgentAdapterForTest()
	copyErr := errors.New("copy failed")
	a.copyDir = func(string, string) error { return copyErr }
	if result, err := a.Install(ref, staging); !errors.Is(err, copyErr) || result.Published {
		t.Fatalf("Install() = %+v, %v, want %v", result, err, copyErr)
	}
	entries, err := os.ReadDir(filepath.Join(root, "agents"))
	if err != nil {
		t.Fatalf("read agents root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("private directories remain after copy failure: %v", entries)
	}
	if _, err := os.Lstat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging not cleaned: %v", err)
	}
}

func TestBibitAgentAdapterRejectsInvalidYAML(t *testing.T) {
	_, staging := newBibitAgentFixture(t, true)
	ref := domain.PowerRef{Name: "x", Type: domain.PowerTypeExpert, Version: "v1.0.0"}
	dir := filepath.Join(staging, "agents", "x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte("schema_version: 1\nagent: {id: x, name: x, version: bad, description: demo, soul: demo}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := newBibitAgentAdapterForTest()
	if _, err := a.Install(ref, staging); err == nil {
		t.Fatal("Install() error = nil, want validation error")
	}
}

func TestBibitAgentInstallRejectsStagingIdentityMismatchWithoutPublishing(t *testing.T) {
	root, staging := newBibitAgentFixture(t, true)
	ref := domain.PowerRef{Type: domain.PowerTypeExpert, Name: "demo", Version: "v1.0.0"}
	writeAgentDirectory(t, staging, ref.Name, []byte("schema_version: 1\nagent: {id: demo, name: demo, version: v2.0.0, description: demo, soul: demo, metadata: {avatar: assets/avatar.png}}\n"))
	a := newBibitAgentAdapterForTest()
	if result, err := a.Install(ref, staging); err == nil || result.Published {
		t.Fatalf("Install() = %+v, %v", result, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "agents", "demo")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists: %v", err)
	}
	if _, err := os.Lstat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging not cleaned: %v", err)
	}
}

func TestBibitAgentValidateArtifactRequiresExactIdentity(t *testing.T) {
	root, _ := newBibitAgentFixture(t, true)
	ref := domain.PowerRef{Type: domain.PowerTypeExpert, Name: "demo", Version: "v1.0.0"}
	writeAgentDirectory(t, root, "demo", []byte(agentFixtureYAML(ref)))
	a := newBibitAgentAdapterForTest()
	path := filepath.Join(root, "agents", "demo")
	if err := a.ValidateArtifact(ref, path); err != nil {
		t.Fatalf("ValidateArtifact() error = %v", err)
	}
	mismatch := ref
	mismatch.Version = "v2.0.0"
	if err := a.ValidateArtifact(mismatch, path); !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("mismatch error = %v", err)
	}
}
