package cache

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"apm/internal/domain"
)

type stagingFailureSource struct{}

func (stagingFailureSource) DownloadSkill(_ context.Context, entry domain.CatalogEntry, destination string) error {
	contents := "---\nname: " + entry.Ref.Name + "\nversion: \"" + entry.Ref.Version + "\"\ndescription: skill\n---\nskill-" + entry.Ref.Version
	return os.WriteFile(filepath.Join(destination, "SKILL.md"), []byte(contents), 0o644)
}

func (stagingFailureSource) DownloadAgent(_ context.Context, entry domain.CatalogEntry, destination string) error {
	if err := os.MkdirAll(filepath.Join(destination, "assets"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(destination, "agent.yaml"), []byte(stagingAgentYAML(entry.Ref)), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destination, "assets", "avatar.png"), internalTestAgentAvatarPNG, 0o644)
}

// stagingAgentYAML 返回与 ref 身份一致、avatar 指向 assets/avatar.png 的有效 Agent fixture。
func stagingAgentYAML(ref domain.PowerRef) string {
	return "schema_version: 1\nagent: {id: " + ref.Name + ", name: " + ref.Name + ", version: " + ref.Version + ", description: demo, soul: demo, metadata: {avatar: assets/avatar.png}}\n"
}

// internalTestAgentAvatarPNG 是 128x128 有效 PNG，满足 avatar 校验。
var internalTestAgentAvatarPNG = func() []byte {
	var output bytes.Buffer
	if err := png.Encode(&output, image.NewNRGBA(image.Rect(0, 0, 128, 128))); err != nil {
		panic("encode test agent avatar PNG: " + err.Error())
	}
	return output.Bytes()
}()

func internalCacheEntry(ref domain.PowerRef) domain.CatalogEntry {
	return domain.CatalogEntry{
		Ref: ref, Description: "catalog description", Message: "catalog message",
		ContentType: "application/gzip", ContentSize: 17,
		ContentMD5:  "0123456789abcdef0123456789abcdef",
		PublishedAt: "2026-08-25T12:34:56Z", Tags: []string{},
	}
}

func newInternalFilesystemCache(t *testing.T) *FilesystemCache {
	t.Helper()
	t.Setenv("APM_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	store, err := NewFilesystem(stagingFailureSource{})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestPrepareSkillStagingFailureRetainsPreviousStaging(t *testing.T) {
	store := newInternalFilesystemCache(t)
	if _, err := store.PrepareSkill(context.Background(), internalCacheEntry(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"})); err != nil {
		t.Fatal(err)
	}
	store.copyDir = func(string, string) error { return errors.New("copy staging candidate") }
	if _, err := store.PrepareSkill(context.Background(), internalCacheEntry(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"})); err == nil {
		t.Fatal("PrepareSkill() error = nil, want staging copy error")
	}
	if got, err := os.ReadFile(filepath.Join(store.stagingPath, "skills", "demo", "SKILL.md")); err != nil || !strings.HasSuffix(string(got), "skill-v1.0.0") {
		t.Fatalf("previous staging = %q, %v, want v1.0.0 payload", got, err)
	}
}

func TestPrepareExpertStagingFailureRetainsPreviousStaging(t *testing.T) {
	store := newInternalFilesystemCache(t)
	if _, err := store.PrepareExpert(context.Background(), internalCacheEntry(domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v1.0.0"})); err != nil {
		t.Fatal(err)
	}
	store.copyDir = func(string, string) error { return errors.New("copy staging candidate") }
	if _, err := store.PrepareExpert(context.Background(), internalCacheEntry(domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v2.0.0"})); err == nil {
		t.Fatal("PrepareExpert() error = nil, want staging copy error")
	}
	for _, relative := range []string{"agent.yaml", "assets/avatar.png"} {
		if _, err := os.Stat(filepath.Join(store.stagingPath, "agents", "demo", relative)); err != nil {
			t.Fatalf("previous staging %q: %v", relative, err)
		}
	}
	want := stagingAgentYAML(domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v1.0.0"})
	if got, err := os.ReadFile(filepath.Join(store.stagingPath, "agents", "demo", "agent.yaml")); err != nil || string(got) != want {
		t.Fatalf("previous staging = %q, %v, want %q", got, err, want)
	}
}

func TestPrepareSkillJoinsCandidateCleanupFailureWithBuildFailure(t *testing.T) {
	store := newInternalFilesystemCache(t)
	if _, err := store.PrepareSkill(context.Background(), internalCacheEntry(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"})); err != nil {
		t.Fatal(err)
	}
	buildErr := errors.New("copy staging candidate")
	cleanupErr := errors.New("remove staging candidate")
	store.copyDir = func(string, string) error { return buildErr }
	store.removeAll = func(string) error { return cleanupErr }
	_, err := store.PrepareSkill(context.Background(), internalCacheEntry(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}))
	if !errors.Is(err, buildErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("PrepareSkill() error = %v, want joined build and cleanup errors", err)
	}
}

func TestPrepareExpertReturnsCleanupFailureAfterAtomicPublication(t *testing.T) {
	store := newInternalFilesystemCache(t)
	if _, err := store.PrepareExpert(context.Background(), internalCacheEntry(domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v1.0.0"})); err != nil {
		t.Fatal(err)
	}
	cleanupErr := errors.New("remove exchanged old staging")
	store.removeAll = func(string) error { return cleanupErr }
	staging, err := store.PrepareExpert(context.Background(), internalCacheEntry(domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v2.0.0"}))
	if !errors.Is(err, cleanupErr) || !strings.Contains(err.Error(), "clean staging candidate") {
		t.Fatalf("PrepareExpert() error = %v, want stable cleanup context", err)
	}
	want := stagingAgentYAML(domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v2.0.0"})
	if got, readErr := os.ReadFile(filepath.Join(staging, "agents", "demo", "agent.yaml")); readErr != nil || string(got) != want {
		t.Fatalf("new staging = %q, %v, want %q", got, readErr, want)
	}
	if _, err := os.Stat(filepath.Join(staging, "agents", "demo", "assets", "avatar.png")); err != nil {
		t.Fatalf("new staging avatar: %v", err)
	}
}

func TestPrepareSkillSuccessfulCandidateCleanupStillReturnsStaging(t *testing.T) {
	store := newInternalFilesystemCache(t)
	if _, err := store.PrepareSkill(context.Background(), internalCacheEntry(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"})); err != nil {
		t.Fatal(err)
	}
	cleanupCalls := 0
	store.removeAll = func(path string) error {
		cleanupCalls++
		return os.RemoveAll(path)
	}
	staging, err := store.PrepareSkill(context.Background(), internalCacheEntry(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}))
	if err != nil {
		t.Fatal(err)
	}
	if cleanupCalls != 1 {
		t.Fatalf("candidate cleanup calls = %d, want 1", cleanupCalls)
	}
	if got, readErr := os.ReadFile(filepath.Join(staging, "skills", "demo", "SKILL.md")); readErr != nil || !strings.HasSuffix(string(got), "skill-v2.0.0") {
		t.Fatalf("new staging = %q, %v, want v2.0.0 payload", got, readErr)
	}
}
