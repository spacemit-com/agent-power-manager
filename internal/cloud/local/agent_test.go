package local

import (
	"bytes"
	"context"
	"errors"
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
	content, err := client.loadCatalogContent(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	entry := content.entry
	destination := filepath.Join(t.TempDir(), "agent.yaml")
	if err := client.DownloadAgent(context.Background(), entry, destination); err != nil {
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

func TestLocalDownloadAgentSanitizesTemporaryModeBeforePublication(t *testing.T) {
	wantContents := []byte("schema_version: 1\nagent: {id: demo, version: v1.0.0, description: demo}\n")
	root := t.TempDir()
	source := filepath.Join(root, "agents", "demo", "v1.0.0", "agent.yaml")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, wantContents, 0o677); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0o677); err != nil {
		t.Fatal(err)
	}
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "agent.yaml")
	observedBeforeWrite := false
	client.beforeAgentWrite = func(temporary *os.File) {
		observedBeforeWrite = true
		assertPrivateAgentStaging(t, temporary)
	}
	observedAfterWrite := false
	client.agentTemporaryChmod = func(temporary *os.File, mode os.FileMode) error {
		observedAfterWrite = true
		assertCompletePrivateAgentStaging(t, temporary, wantContents)
		return temporary.Chmod(mode)
	}
	client.beforeAgentPublish = func() {
		matches, globErr := filepath.Glob(filepath.Join(filepath.Dir(destination), ".agent.yaml.tmp-*"))
		if globErr != nil {
			t.Fatal(globErr)
		}
		if len(matches) != 1 {
			t.Fatalf("staged Agent files = %v, want one", matches)
		}
		assertPublishedAgentFile(t, matches[0])
	}
	t.Cleanup(func() {
		client.beforeAgentWrite = nil
		client.agentTemporaryChmod = nil
		client.beforeAgentPublish = nil
	})

	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	content, err := client.loadCatalogContent(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DownloadAgent(context.Background(), content.entry, destination); err != nil {
		t.Fatalf("DownloadAgent() error = %v", err)
	}
	if !observedBeforeWrite {
		t.Fatal("Agent temporary file was not observed before write")
	}
	if !observedAfterWrite {
		t.Fatal("Agent temporary file was not observed after write")
	}
	assertPublishedAgentFile(t, destination)
}

func TestLocalDownloadAgentCleansTemporaryAfterChmodFailure(t *testing.T) {
	wantContents := []byte("schema_version: 1\nagent: {id: demo, version: v1.0.0, description: demo}\n")
	root := t.TempDir()
	source := filepath.Join(root, "agents", "demo", "v1.0.0", "agent.yaml")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, wantContents, 0o644); err != nil {
		t.Fatal(err)
	}
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	destination := filepath.Join(parent, "agent.yaml")
	chmodErr := errors.New("chmod failed")
	client.agentTemporaryChmod = func(temporary *os.File, _ os.FileMode) error {
		assertCompletePrivateAgentStaging(t, temporary, wantContents)
		return chmodErr
	}
	t.Cleanup(func() { client.agentTemporaryChmod = nil })

	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	content, err := client.loadCatalogContent(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	err = client.DownloadAgent(context.Background(), content.entry, destination)
	if !errors.Is(err, chmodErr) {
		t.Fatalf("DownloadAgent() error = %v, want chmod failure", err)
	}
	entries, readErr := os.ReadDir(parent)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("Agent chmod failure cleanup left entries: %v", entries)
	}
}

func assertCompletePrivateAgentStaging(t *testing.T, temporary *os.File, want []byte) {
	t.Helper()
	assertPrivateAgentStaging(t, temporary)
	info, err := temporary.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(len(want)) {
		t.Fatalf("Agent temporary size at chmod = %d, want %d", info.Size(), len(want))
	}
	got := make([]byte, len(want))
	n, err := temporary.ReadAt(got, 0)
	if err != nil {
		t.Fatalf("read Agent temporary at chmod: %v", err)
	}
	if n != len(want) {
		t.Fatalf("Agent temporary bytes read at chmod = %d, want %d", n, len(want))
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Agent temporary contents at chmod = %q, want %q", got, want)
	}
}

func TestLocalClientDownloadAgentRejectsWrongType(t *testing.T) {
	client := newAgentTestClient(t)
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeSkill, Version: "v1.0.0"}
	if err := client.DownloadAgent(context.Background(), domain.CatalogEntry{Ref: ref}, filepath.Join(t.TempDir(), "agent.yaml")); !errors.Is(err, cloud.ErrNotFound) {
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
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	content, err := client.loadCatalogContent(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		result <- client.DownloadAgent(context.Background(), content.entry, destination)
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
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	content, err := client.loadCatalogContent(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	err = client.DownloadAgent(context.Background(), content.entry, destination)
	if err == nil {
		t.Fatal("DownloadAgent() error = nil, want existing destination error")
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != "old" {
		t.Fatalf("destination changed = %q, %v", got, err)
	}
}
