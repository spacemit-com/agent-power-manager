package local

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
)

func newAgentTestClient(t *testing.T) *Client {
	t.Helper()
	resources, err := filepath.Abs("../resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(resources)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// localAgentFixtureYAML 是 V0.5 有效的 Agent fixture 模板：id 为 Bibit 内部身份，
// name 与版本目录一致，avatar 指向 assets/avatar.png。
const localAgentFixtureYAML = "schema_version: 1\nagent:\n  id: bibit-internal-demo\n  name: demo\n  version: v1.0.0\n  description: demo agent\n  soul: help safely\n  metadata:\n    avatar: assets/avatar.png\n"

// writeLocalAgentFixture 写出版本目录结构的 Agent fixture，并通过 mutate 追加或改写。
func writeLocalAgentFixture(t *testing.T, root string, mutate func(*testing.T, string)) domain.PowerRef {
	t.Helper()
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	source := filepath.Join(root, "agents", ref.Name, ref.Version)
	writeTestBytes(t, filepath.Join(source, "agent.yaml"), []byte(localAgentFixtureYAML))
	writeTestBytes(t, filepath.Join(source, "assets", "avatar.png"), testPNGBytes(t, 128))
	if mutate != nil {
		mutate(t, source)
	}
	return ref
}

func testPNGBytes(t *testing.T, side int) []byte {
	t.Helper()
	var output bytes.Buffer
	canvas := image.NewNRGBA(image.Rect(0, 0, side, side))
	for index := 0; index < len(canvas.Pix); index += 4 {
		canvas.Pix[index+3] = 0xff
	}
	if err := png.Encode(&output, canvas); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestLocalDownloadAgentPublishesDirectoryTreeWithoutGlobalCache(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	client := newAgentTestClient(t)
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	content, err := client.loadCatalogContent(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if content.entry.ContentType != "application/gzip" {
		t.Fatalf("Agent catalog content type = %q, want application/gzip", content.entry.ContentType)
	}
	destination := filepath.Join(t.TempDir(), "agent")
	if err := client.DownloadAgent(context.Background(), content.entry, destination); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(destination, "agent.yaml"))
	if err != nil || len(raw) == 0 {
		t.Fatalf("downloaded agent.yaml = %q, %v; want non-empty", raw, err)
	}
	config, err := domain.DecodeAgentConfigYAML(raw)
	if err != nil {
		t.Fatalf("decode downloaded agent.yaml: %v", err)
	}
	if config.Agent.Name != ref.Name || config.Agent.Version != ref.Version {
		t.Fatalf("downloaded identity = %s@%s, want %s@%s", config.Agent.Name, config.Agent.Version, ref.Name, ref.Version)
	}
	avatar, err := os.ReadFile(filepath.Join(destination, "assets", "avatar.png"))
	if err != nil {
		t.Fatalf("read downloaded avatar: %v", err)
	}
	if !bytes.HasPrefix(avatar, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("downloaded avatar is not a PNG")
	}
	if _, err := os.Lstat(filepath.Join(cacheHome, "apm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Local provider touched global cache: %v", err)
	}
}

func TestLocalDownloadAgentPublishesSanitizedModes(t *testing.T) {
	root := t.TempDir()
	ref := writeLocalAgentFixture(t, root, func(t *testing.T, source string) {
		writeTestBytes(t, filepath.Join(source, "assets", "data.bin"), []byte{'a', 0, 'b'})
	})
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	content, err := client.loadCatalogContent(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "agent")
	if err := client.DownloadAgent(context.Background(), content.entry, destination); err != nil {
		t.Fatal(err)
	}
	expectations := map[string]publishedNodeExpectation{
		".":                              {mode: 0o755, directory: true},
		"assets":                         {mode: 0o755, directory: true},
		"agent.yaml":                     {mode: 0o644},
		filepath.Join("assets", "avatar.png"): {mode: 0o644},
		filepath.Join("assets", "data.bin"):   {mode: 0o644},
	}
	assertPublishedTree(t, destination, expectations)
}

func TestLocalDownloadAgentAllowsBinaryNonAvatarAsset(t *testing.T) {
	root := t.TempDir()
	ref := writeLocalAgentFixture(t, root, func(t *testing.T, source string) {
		writeTestBytes(t, filepath.Join(source, "assets", "payload.bin"), []byte{0x00, 0x01, 0xfe, 0xff})
	})
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	content, err := client.loadCatalogContent(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "agent")
	if err := client.DownloadAgent(context.Background(), content.entry, destination); err != nil {
		t.Fatalf("DownloadAgent() with binary non-avatar asset error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "assets", "payload.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{0x00, 0x01, 0xfe, 0xff}) {
		t.Fatalf("published binary asset = %v, want original bytes", got)
	}
}

func TestLocalDownloadAgentRejectsInvalidAvatarWithoutDestination(t *testing.T) {
	root := t.TempDir()
	writeLocalAgentFixture(t, root, func(t *testing.T, source string) {
		writeTestBytes(t, filepath.Join(source, "assets", "avatar.png"), []byte("not a png"))
	})
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "agent")
	err = client.DownloadAgent(context.Background(), catalogEntryForTest(domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}), destination)
	assertUnsafeNoDownloadResidue(t, err, destination)
}

func TestLocalDownloadAgentRejectsExtraRootFileWithoutDestination(t *testing.T) {
	root := t.TempDir()
	writeLocalAgentFixture(t, root, func(t *testing.T, source string) {
		writeTestFile(t, filepath.Join(source, "README.md"), "extra")
	})
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "agent")
	err = client.DownloadAgent(context.Background(), catalogEntryForTest(domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}), destination)
	assertUnsafeNoDownloadResidue(t, err, destination)
}

func TestLocalDownloadAgentCancellationBeforePublishLeavesNoDestination(t *testing.T) {
	root := t.TempDir()
	ref := writeLocalAgentFixture(t, root, nil)
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	content, err := client.loadCatalogContent(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	client.beforeAgentPublish = cancel
	t.Cleanup(func() { client.beforeAgentPublish = nil })
	destination := filepath.Join(t.TempDir(), "agent")

	err = client.DownloadAgent(ctx, content.entry, destination)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DownloadAgent() error = %v, want context.Canceled", err)
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination stat error = %v, want absent", statErr)
	}
	assertNoSiblingTemps(t, destination)
}

func TestLocalDownloadAgentRejectsExistingNonEmptyDestination(t *testing.T) {
	root := t.TempDir()
	ref := writeLocalAgentFixture(t, root, nil)
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	content, err := client.loadCatalogContent(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	destination := filepath.Join(parent, "agent")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = client.DownloadAgent(context.Background(), content.entry, destination)
	if !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("DownloadAgent() error = %v, want ErrUnsafeContent", err)
	}
	if got, readErr := os.ReadFile(filepath.Join(destination, "keep")); readErr != nil || string(got) != "keep" {
		t.Fatalf("pre-existing destination changed: %q, %v", got, readErr)
	}
	assertNoSiblingTemps(t, destination)
}

func TestLocalDownloadAgentAcceptsExistingEmptyDestination(t *testing.T) {
	root := t.TempDir()
	ref := writeLocalAgentFixture(t, root, nil)
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	content, err := client.loadCatalogContent(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "agent")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := client.DownloadAgent(context.Background(), content.entry, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "agent.yaml")); err != nil {
		t.Fatalf("published into existing empty destination: %v", err)
	}
}

func TestLocalClientDownloadAgentRejectsWrongType(t *testing.T) {
	client := newAgentTestClient(t)
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeSkill, Version: "v1.0.0"}
	if err := client.DownloadAgent(context.Background(), domain.CatalogEntry{Ref: ref}, filepath.Join(t.TempDir(), "agent")); !errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("DownloadAgent() error = %v, want ErrNotFound", err)
	}
}
