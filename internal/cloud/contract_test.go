package cloud_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"apm/internal/cloud"
	"apm/internal/cloud/local"
	"apm/internal/cloud/remote"
	"apm/internal/domain"
)

type clientFactory struct {
	name string
	open func(t *testing.T) cloud.Client
}

func TestProviderContract(t *testing.T) {
	factories := providerFactories(t)
	for _, factory := range factories {
		t.Run(factory.name, func(t *testing.T) {
			runProviderContract(t, factory.open(t))
		})
	}
	t.Run("local_remote_parity", func(t *testing.T) {
		compareProviderOutcomes(t, factories[0].open(t), factories[1].open(t))
	})
}

func providerFactories(t *testing.T) []clientFactory {
	t.Helper()
	trustedParent := os.Getenv("XDG_RUNTIME_DIR")
	var err error
	if trustedParent == "" {
		trustedParent, err = os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
	}
	trustedTemp, err := os.MkdirTemp(trustedParent, ".provider-contract-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(trustedTemp, 0o700); err != nil {
		t.Fatal(err)
	}
	trustedTemp, err = filepath.Abs(trustedTemp)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(trustedTemp); err != nil {
			t.Errorf("remove trusted provider-contract temp: %v", err)
		}
	})
	t.Setenv("TMPDIR", trustedTemp)
	root, err := filepath.Abs("resources")
	if err != nil {
		t.Fatal(err)
	}
	server := newCanonicalRemoteServer(t, root)
	return []clientFactory{
		{name: "local", open: func(t *testing.T) cloud.Client {
			client, err := local.New(root)
			if err != nil {
				t.Fatal(err)
			}
			return client
		}},
		{name: "remote", open: func(t *testing.T) cloud.Client {
			client, err := remote.New(server.server.URL, &http.Client{Timeout: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			return client
		}},
	}
}

func runProviderContract(t *testing.T, client cloud.Client) {
	t.Helper()
	refs, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) == 0 {
		t.Fatal("List() returned no committed fixtures")
	}
	wantSorted := append([]domain.PowerRef(nil), refs...)
	sort.Slice(wantSorted, func(left, right int) bool {
		if wantSorted[left].Type != wantSorted[right].Type {
			return wantSorted[left].Type < wantSorted[right].Type
		}
		if wantSorted[left].Name != wantSorted[right].Name {
			return wantSorted[left].Name < wantSorted[right].Name
		}
		return versionTuple(wantSorted[left].Version) < versionTuple(wantSorted[right].Version)
	})
	if !reflect.DeepEqual(refs, wantSorted) {
		t.Fatalf("List() is not stable type/name/SemVer order:\n got: %#v\nwant: %#v", refs, wantSorted)
	}

	latest := make(map[[2]string]domain.PowerRef)
	for _, ref := range refs {
		got, err := client.Resolve(context.Background(), ref)
		if err != nil || got != ref {
			t.Fatalf("Resolve(exact %+v) = %+v, %v", ref, got, err)
		}
		latest[[2]string{string(ref.Type), ref.Name}] = ref
		assertPowerReadable(t, client, ref)
	}
	for _, want := range latest {
		query := domain.PowerRef{Type: want.Type, Name: want.Name}
		got, err := client.Resolve(context.Background(), query)
		if err != nil || got != want {
			t.Fatalf("Resolve(latest %+v) = %+v, %v; want %+v", query, got, err, want)
		}
	}
	for _, powerType := range []domain.PowerType{
		domain.PowerTypeSkill, domain.PowerTypeMCP, domain.PowerTypePlugin, domain.PowerTypeAgent,
	} {
		_, err := client.Resolve(context.Background(), domain.PowerRef{
			Type: powerType, Name: "contract-exact-miss", Version: "v9.9.9",
		})
		if !errors.Is(err, cloud.ErrNotFound) {
			t.Fatalf("Resolve(%s exact miss) error = %v, want ErrNotFound", powerType, err)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.List(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("List(cancelled) error = %v, want context.Canceled", err)
	}
	if _, err := client.Resolve(cancelled, refs[0]); !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve(cancelled) error = %v, want context.Canceled", err)
	}
}

func assertPowerReadable(t *testing.T, client cloud.Client, ref domain.PowerRef) {
	t.Helper()
	switch ref.Type {
	case domain.PowerTypeSkill:
		if err := client.DownloadSkill(context.Background(), ref, filepath.Join(privateTestDir(t), "skill")); err != nil {
			t.Fatalf("DownloadSkill(%+v): %v", ref, err)
		}
	case domain.PowerTypePlugin:
		if err := client.DownloadPlugin(context.Background(), ref, filepath.Join(privateTestDir(t), "plugin")); err != nil {
			t.Fatalf("DownloadPlugin(%+v): %v", ref, err)
		}
	case domain.PowerTypeMCP:
		if _, err := client.GetMCP(context.Background(), ref); err != nil {
			t.Fatalf("GetMCP(%+v): %v", ref, err)
		}
	case domain.PowerTypeAgent:
		if err := client.DownloadAgent(context.Background(), ref, filepath.Join(privateTestDir(t), "agent.yaml")); err != nil {
			t.Fatalf("DownloadAgent(%+v): %v", ref, err)
		}
	default:
		t.Fatalf("unsupported Power type %q", ref.Type)
	}
}

func compareProviderOutcomes(t *testing.T, localClient, remoteClient cloud.Client) {
	t.Helper()
	localRefs, err := localClient.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	remoteRefs, err := remoteClient.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(localRefs, remoteRefs) {
		t.Fatalf("List parity:\nlocal:  %#v\nremote: %#v", localRefs, remoteRefs)
	}

	seenLatest := make(map[[2]string]bool)
	for _, ref := range localRefs {
		localExact, localErr := localClient.Resolve(context.Background(), ref)
		remoteExact, remoteErr := remoteClient.Resolve(context.Background(), ref)
		if localErr != nil || remoteErr != nil || localExact != remoteExact {
			t.Fatalf("exact Resolve parity for %+v: local=%+v/%v remote=%+v/%v", ref, localExact, localErr, remoteExact, remoteErr)
		}
		key := [2]string{string(ref.Type), ref.Name}
		if !seenLatest[key] {
			query := domain.PowerRef{Type: ref.Type, Name: ref.Name}
			localLatest, localErr := localClient.Resolve(context.Background(), query)
			remoteLatest, remoteErr := remoteClient.Resolve(context.Background(), query)
			if localErr != nil || remoteErr != nil || localLatest != remoteLatest {
				t.Fatalf("latest Resolve parity for %+v: local=%+v/%v remote=%+v/%v", query, localLatest, localErr, remoteLatest, remoteErr)
			}
			seenLatest[key] = true
		}
		comparePowerOutcome(t, localClient, remoteClient, ref)
	}
}

func comparePowerOutcome(t *testing.T, localClient, remoteClient cloud.Client, ref domain.PowerRef) {
	t.Helper()
	switch ref.Type {
	case domain.PowerTypeSkill, domain.PowerTypePlugin:
		localTarget := filepath.Join(privateTestDir(t), "local")
		remoteTarget := filepath.Join(privateTestDir(t), "remote")
		var localErr, remoteErr error
		if ref.Type == domain.PowerTypeSkill {
			localErr = localClient.DownloadSkill(context.Background(), ref, localTarget)
			remoteErr = remoteClient.DownloadSkill(context.Background(), ref, remoteTarget)
		} else {
			localErr = localClient.DownloadPlugin(context.Background(), ref, localTarget)
			remoteErr = remoteClient.DownloadPlugin(context.Background(), ref, remoteTarget)
		}
		if localErr != nil || remoteErr != nil {
			t.Fatalf("download parity for %+v: local=%v remote=%v", ref, localErr, remoteErr)
		}
		localTree := normalizedTree(t, localTarget)
		remoteTree := normalizedTree(t, remoteTarget)
		if !reflect.DeepEqual(localTree, remoteTree) {
			t.Fatalf("tree parity for %+v:\nlocal:  %#v\nremote: %#v", ref, localTree, remoteTree)
		}
	case domain.PowerTypeMCP:
		localServer, localErr := localClient.GetMCP(context.Background(), ref)
		remoteServer, remoteErr := remoteClient.GetMCP(context.Background(), ref)
		if localErr != nil || remoteErr != nil || !reflect.DeepEqual(localServer, remoteServer) {
			t.Fatalf("MCP parity for %+v: local=%#v/%v remote=%#v/%v", ref, localServer, localErr, remoteServer, remoteErr)
		}
	case domain.PowerTypeAgent:
		localTarget := filepath.Join(privateTestDir(t), "agent.yaml")
		remoteTarget := filepath.Join(privateTestDir(t), "agent.yaml")
		localErr := localClient.DownloadAgent(context.Background(), ref, localTarget)
		remoteErr := remoteClient.DownloadAgent(context.Background(), ref, remoteTarget)
		if localErr != nil || remoteErr != nil {
			t.Fatalf("Agent parity for %+v: local=%v remote=%v", ref, localErr, remoteErr)
		}
		localFile := normalizedFile(t, localTarget, "agent.yaml")
		remoteFile := normalizedFile(t, remoteTarget, "agent.yaml")
		if !reflect.DeepEqual(localFile, remoteFile) {
			t.Fatalf("Agent parity for %+v: local=%#v remote=%#v", ref, localFile, remoteFile)
		}
	}
}

type normalizedNode struct {
	Path  string
	Mode  fs.FileMode
	Bytes string
}

func normalizedTree(t *testing.T, root string) []normalizedNode {
	t.Helper()
	var nodes []normalizedNode
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		node := normalizedNode{Path: filepath.ToSlash(relative)}
		if entry.IsDir() {
			node.Mode = 0o755
		} else {
			node.Mode = 0o644 | info.Mode().Perm()&0o111
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			node.Bytes = string(contents)
		}
		nodes = append(nodes, node)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return nodes
}

func normalizedFile(t *testing.T, path, logicalPath string) normalizedNode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return normalizedNode{Path: logicalPath, Mode: 0o644 | info.Mode().Perm()&0o111, Bytes: string(contents)}
}

func privateTestDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp(os.TempDir(), "provider-contract-case-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove provider contract case directory: %v", err)
		}
	})
	return directory
}

func versionTuple(version string) string {
	var major, minor, patch int
	if _, err := fmt.Sscanf(version, "v%d.%d.%d", &major, &minor, &patch); err != nil {
		return version
	}
	return fmt.Sprintf("%010d/%010d/%010d", major, minor, patch)
}
