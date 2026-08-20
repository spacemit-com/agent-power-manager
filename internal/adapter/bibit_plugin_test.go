package adapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

func TestBibitPluginAdapterUsesLogicalDirectoryAndNoCLIActivation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BIANBU_AGENT_HOME", root)
	adapter := newBibitPluginAdapter(bibitPluginProfile())

	target, err := adapter.TargetPath("demo-plugin")
	if err != nil || target != filepath.Join(root, "plugins", "demo-plugin") {
		t.Fatalf("TargetPath() = %q, %v", target, err)
	}
	absent, err := adapter.Inspect(context.Background(), "demo-plugin")
	if err != nil || absent != (PluginFact{Presence: PluginAbsent, ActualName: "demo-plugin"}) {
		t.Fatalf("Inspect() = %+v, %v", absent, err)
	}
	for _, mutate := range []func(context.Context, PluginTarget) (PluginMutationResult, error){adapter.Add, adapter.Remove} {
		got, err := mutate(context.Background(), PluginTarget{ActualName: "demo-plugin", Selector: "demo-plugin"})
		if err != nil || got.State != PluginMutationApplied {
			t.Fatalf("mutation = %+v, %v", got, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "plugins")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("activation hooks changed filesystem: Lstat() error = %v, want not exist", err)
	}
}

func TestBibitPluginAdapterInspectsOnlyPhysicalDirectories(t *testing.T) {
	for _, test := range []struct {
		name     string
		create   func(t *testing.T, target string)
		wantErr  error
		wantFact PluginFact
	}{
		{
			name: "directory is present",
			create: func(t *testing.T, target string) {
				t.Helper()
				if err := os.MkdirAll(target, 0o755); err != nil {
					t.Fatal(err)
				}
			},
			wantFact: PluginFact{Presence: PluginPresent, ActualName: "demo-plugin", Selector: "demo-plugin"},
		},
		{
			name: "regular file is unknown",
			create: func(t *testing.T, target string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte("not a plugin directory"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: ErrPluginStateUnknown,
		},
		{
			name: "directory symlink is unknown",
			create: func(t *testing.T, target string) {
				t.Helper()
				physicalDirectory := filepath.Join(t.TempDir(), "physical-plugin")
				if err := os.Mkdir(physicalDirectory, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(physicalDirectory, target); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: ErrPluginStateUnknown,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("BIANBU_AGENT_HOME", root)
			target := filepath.Join(root, "plugins", "demo-plugin")
			test.create(t, target)

			got, err := newBibitPluginAdapter(bibitPluginProfile()).Inspect(context.Background(), "demo-plugin")
			if !errors.Is(err, test.wantErr) || got != test.wantFact {
				t.Fatalf("Inspect() = %+v, %v; want %+v, %v", got, err, test.wantFact, test.wantErr)
			}
		})
	}
}

func TestBibitPluginAdapterRejectsUnsafeNamesAndSelectorMismatches(t *testing.T) {
	adapter := newBibitPluginAdapter(bibitPluginProfile())
	for _, name := range []string{"", ".", "..", "nested/demo", `nested\demo`} {
		t.Run("name="+name, func(t *testing.T) {
			if _, err := adapter.TargetPath(name); !errors.Is(err, ErrInvalidPlugin) {
				t.Fatalf("TargetPath(%q) error = %v, want ErrInvalidPlugin", name, err)
			}
			if _, err := adapter.Inspect(context.Background(), name); !errors.Is(err, ErrInvalidPlugin) {
				t.Fatalf("Inspect(%q) error = %v, want ErrInvalidPlugin", name, err)
			}
		})
	}

	for _, target := range []PluginTarget{
		{ActualName: "../demo-plugin", Selector: "../demo-plugin"},
		{ActualName: "demo-plugin", Selector: "demo-plugin@apm"},
	} {
		for _, mutate := range []struct {
			name string
			fn   func(context.Context, PluginTarget) (PluginMutationResult, error)
		}{
			{name: "add", fn: adapter.Add},
			{name: "remove", fn: adapter.Remove},
		} {
			t.Run(mutate.name+"-"+target.ActualName+"-"+target.Selector, func(t *testing.T) {
				got, err := mutate.fn(context.Background(), target)
				if !errors.Is(err, ErrInvalidPlugin) || got.State != PluginMutationNotApplied {
					t.Fatalf("mutation(%+v) = %+v, %v; want not-applied, ErrInvalidPlugin", target, got, err)
				}
			})
		}
	}
}

func bibitPluginProfile() agentProfile {
	return agentProfile{id: domain.AgentBibit, homeEnv: "BIANBU_AGENT_HOME", defaultRoot: "bianbu-agent"}
}
