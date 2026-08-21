package cloud_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
	"apm/internal/paths"
)

func newAgentTestClient(t *testing.T) *cloud.LocalClient {
	t.Helper()
	resources, err := filepath.Abs("resources")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	client, err := cloud.NewLocalClient(resources)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func agentCachePath(t *testing.T, ref domain.PowerRef) string {
	t.Helper()
	cache, err := paths.CachePath()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(cache, "agents", ref.Name, ref.Version, "agent.yaml")
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

func TestLocalClientDownloadsSingleAgentYAML(t *testing.T) {
	client := newAgentTestClient(t)
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	if err := client.DownloadAgent(ref); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(agentCachePath(t, ref))
	if err != nil {
		t.Fatalf("read cached agent: %v", err)
	}
	if len(contents) == 0 {
		t.Fatal("cached agent is empty")
	}
	if err := client.DownloadAgent(ref); err != nil {
		t.Fatalf("second DownloadAgent() error = %v", err)
	}
}

func TestLocalClientDownloadAgentRejectsWrongType(t *testing.T) {
	client := newAgentTestClient(t)
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeSkill, Version: "v1.0.0"}
	if err := client.DownloadAgent(ref); !errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("DownloadAgent() error = %v, want ErrNotFound", err)
	}
}
