package cache_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"apm/internal/cache"
	"apm/internal/domain"
)

// walkAgentTree 收集 root 下所有文件的规范相对路径到正文的映射，用于精确目录树断言。
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

func TestPrepareAgentPublishesCompleteDirectoryAndStaging(t *testing.T) {
	_, cacheHome := setCacheEnvironment(t)
	store, err := cache.NewFilesystem(&recordingSource{})
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	staging, err := store.PrepareAgent(context.Background(), cacheCatalogEntry(ref))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]byte{
		"agent.yaml":        []byte(agentFixtureYAML(ref)),
		"assets/avatar.png": testAgentAvatarPNG,
	}
	if got := walkAgentTree(t, filepath.Join(cacheHome, "apm", "agents", "demo", "v1.0.0")); !reflect.DeepEqual(got, want) {
		t.Fatalf("cache tree = %v, want %v", got, want)
	}
	if got := walkAgentTree(t, filepath.Join(staging, "agents", "demo")); !reflect.DeepEqual(got, want) {
		t.Fatalf("staging tree = %v, want %v", got, want)
	}
}

func TestPrepareAgentDownloadsMatchingIdentity(t *testing.T) {
	_, cacheHome := setCacheEnvironment(t)
	custom := []byte("schema_version: 1\nagent: {id: custom, name: custom, version: v2.3.4, description: custom, soul: custom, metadata: {avatar: assets/avatar.png}}\n")
	source := &recordingSource{agentContents: custom}
	store, err := cache.NewFilesystem(source)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "custom", Version: "v2.3.4"}
	staging, err := store.PrepareAgent(context.Background(), cacheCatalogEntry(ref))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]byte{
		"agent.yaml":        custom,
		"assets/avatar.png": testAgentAvatarPNG,
	}
	if got := walkAgentTree(t, filepath.Join(cacheHome, "apm", "agents", "custom", "v2.3.4")); !reflect.DeepEqual(got, want) {
		t.Fatalf("cache tree = %v, want %v", got, want)
	}
	if got := walkAgentTree(t, filepath.Join(staging, "agents", "custom")); !reflect.DeepEqual(got, want) {
		t.Fatalf("staging tree = %v, want %v", got, want)
	}
	if source.agentCalls != 1 {
		t.Fatalf("agent calls = %d, want 1", source.agentCalls)
	}
}
