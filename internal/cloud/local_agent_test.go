package cloud

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

func newAgentTestClient(t *testing.T) *LocalClient {
	t.Helper()
	resources, err := filepath.Abs("resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewLocalClient(resources)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestLocalClientListsAgents(t *testing.T) {
	client := newAgentTestClient(t)
	refs, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	for _, ref := range refs {
		if ref == want {
			return
		}
	}
	t.Fatalf("List() = %#v, missing %#v", refs, want)
}

func TestLocalDownloadAgentWritesDestinationWithoutGlobalCache(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	client := newAgentTestClient(t)
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	destination := filepath.Join(t.TempDir(), "agent.yaml")
	if err := client.DownloadAgent(context.Background(), ref, destination); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read cached agent: %v", err)
	}
	if len(contents) == 0 {
		t.Fatal("cached agent is empty")
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("agent destination missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(cacheHome, "apm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Local provider touched global cache: %v", err)
	}
}

func TestLocalClientDownloadAgentRejectsWrongType(t *testing.T) {
	client := newAgentTestClient(t)
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeSkill, Version: "v1.0.0"}
	if err := client.DownloadAgent(context.Background(), ref, filepath.Join(t.TempDir(), "agent.yaml")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DownloadAgent() error = %v, want ErrNotFound", err)
	}
}

func TestLocalDownloadAgentDoesNotReplaceConcurrentDestination(t *testing.T) {
	client := newAgentTestClient(t)
	destination := filepath.Join(t.TempDir(), "agent.yaml")
	reachedPublish := make(chan struct{})
	allowPublish := make(chan struct{})
	client.beforeAgentPublish = func() {
		close(reachedPublish)
		<-allowPublish
	}
	t.Cleanup(func() { client.beforeAgentPublish = nil })

	result := make(chan error, 1)
	go func() {
		result <- client.DownloadAgent(context.Background(), domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"}, destination)
	}()
	<-reachedPublish
	if err := os.WriteFile(destination, []byte("racer"), 0o644); err != nil {
		t.Fatal(err)
	}
	close(allowPublish)
	if err := <-result; err == nil {
		t.Fatal("DownloadAgent() error = nil, want no-replace publication error")
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != "racer" {
		t.Fatalf("concurrent destination = %q, %v, want racer", got, err)
	}
}

func TestLocalDownloadAgentRejectsExistingDestination(t *testing.T) {
	client := newAgentTestClient(t)
	destination := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(destination, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := client.DownloadAgent(context.Background(), domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"}, destination)
	if err == nil {
		t.Fatal("DownloadAgent() error = nil, want existing destination error")
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != "old" {
		t.Fatalf("destination changed = %q, %v", got, err)
	}
}
