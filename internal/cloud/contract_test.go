package cloud_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"apm/internal/cli"
	"apm/internal/cloud"
	"apm/internal/cloud/local"
	"apm/internal/cloud/remote"
	"apm/internal/domain"
	"apm/internal/lifecycle"
	"github.com/Masterminds/semver/v3"
)

type clientFactory struct {
	name string
	open func(t *testing.T) cloud.Client
}

var canonicalPowerOracle = []domain.PowerRef{
	{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"},
	{Name: "company-tools", Type: domain.PowerTypeMCP, Version: "v0.0.1"},
	{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v0.0.1"},
	{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v0.1.0"},
	{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.0.1"},
	{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"},
	{Name: "brief-status", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	{Name: "checklist-first", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	{Name: "code-comment-cn", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	{Name: "commit-message-helper", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	{Name: "commit-message-helper", Type: domain.PowerTypeSkill, Version: "v0.1.0"},
	{Name: "error-context", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	{Name: "foo-before-summary", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	{Name: "foo-before-summary", Type: domain.PowerTypeSkill, Version: "v0.1.0"},
	{Name: "meeting-notes", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	{Name: "plain-language", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	{Name: "test-case-namer", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	{Name: "test-case-namer", Type: domain.PowerTypeSkill, Version: "v0.1.0"},
	{Name: "worklog-entry", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
}

func TestProviderContract(t *testing.T) {
	factories, _ := providerFactories(t)
	for _, factory := range factories {
		t.Run(factory.name, func(t *testing.T) {
			runProviderContract(t, factory.open(t))
		})
	}
	t.Run("local_remote_parity", func(t *testing.T) {
		compareProviderOutcomes(t, factories[0].open(t), factories[1].open(t))
	})
	t.Run("oracle_mutations", testCanonicalPowerOracleMutations)
	t.Run("wire_mutations", testCanonicalRemoteServerProtocolMutations)
}

func TestProviderContractRejectsIncompleteSkillPackage(t *testing.T) {
	root := privateTestDir(t)
	ref := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "incomplete-skill", Version: "v1.0.0"}
	source := filepath.Join(root, "skills", ref.Name, ref.Version)
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	localClient, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	remoteFixture := &canonicalRemoteServer{resources: []canonicalRemoteResource{{
		ref: ref, contentType: "application/gzip", content: deterministicArchive(t, source),
	}}}
	server := httptest.NewServer(http.HandlerFunc(remoteFixture.serveHTTP))
	t.Cleanup(server.Close)
	remoteClient, err := remote.New(server.URL, &http.Client{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}

	for _, provider := range []struct {
		name   string
		client cloud.Client
	}{{name: "local", client: localClient}, {name: "remote", client: remoteClient}} {
		t.Run(provider.name, func(t *testing.T) {
			destinationRoot := privateTestDir(t)
			destination := filepath.Join(destinationRoot, "skill")
			err := provider.client.DownloadSkill(context.Background(), ref, destination)
			assertExclusiveContractError(t, err, cloud.ErrUnsafeContent)
			assertNoDownloadResidue(t, destinationRoot, destination)
		})
	}
}

func TestProviderFactoriesUseTestingTempRoot(t *testing.T) {
	fakeRoot := t.TempDir()
	if err := os.Chmod(fakeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	missingHome := filepath.Join(fakeRoot, "missing-home")
	var root string
	t.Run("create", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "")
		t.Setenv("HOME", missingHome)
		t.Setenv("GOTMPDIR", fakeRoot)
		t.Setenv("TMPDIR", fakeRoot)
		_, root = providerFactories(t)
		assertCloudPathWithinTestRoot(t, fakeRoot, root)
	})
	if root == "" {
		return
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("provider-contract root remains after cleanup: %v", err)
	}
}

func assertCloudPathWithinTestRoot(t *testing.T, root, path string) {
	t.Helper()
	relative, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("provider-contract path %q is outside fake root %q", path, root)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o700 {
		t.Fatalf("provider-contract path permissions = %04o, want 0700", permissions)
	}
}

func providerFactories(t *testing.T) ([]clientFactory, string) {
	t.Helper()
	trustedTemp := t.TempDir()
	if err := os.Chmod(trustedTemp, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", trustedTemp)
	t.Setenv("XDG_RUNTIME_DIR", trustedTemp)
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
	}, trustedTemp
}

func runProviderContract(t *testing.T, client cloud.Client) {
	t.Helper()
	refs, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := compareRefsToOracle(refs); err != nil {
		t.Fatalf("List() does not match the committed fixture oracle: %v", err)
	}

	for _, ref := range canonicalPowerOracle {
		got, err := client.Resolve(context.Background(), ref)
		if err != nil || got != ref {
			t.Fatalf("Resolve(exact %+v) = %+v, %v", ref, got, err)
		}
		assertPowerReadable(t, client, ref)
	}
	latest, err := latestPowerRefs(canonicalPowerOracle)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range latest {
		query := domain.PowerRef{Type: want.Type, Name: want.Name}
		got, err := client.Resolve(context.Background(), query)
		if err != nil || got != want {
			t.Fatalf("Resolve(latest %+v) = %+v, %v; want %+v", query, got, err, want)
		}
	}
	assertClientOperationErrors(t, client)
}

func compareRefsToOracle(got []domain.PowerRef) error {
	if !reflect.DeepEqual(got, canonicalPowerOracle) {
		return fmt.Errorf("got %#v, want %#v", got, canonicalPowerOracle)
	}
	return nil
}

func latestPowerRefs(refs []domain.PowerRef) ([]domain.PowerRef, error) {
	type powerKey struct {
		powerType domain.PowerType
		name      string
	}
	latest := make(map[powerKey]domain.PowerRef)
	versions := make(map[powerKey]*semver.Version)
	for _, ref := range refs {
		parsed, err := semver.StrictNewVersion(strings.TrimPrefix(ref.Version, "v"))
		if err != nil || "v"+parsed.String() != ref.Version {
			return nil, fmt.Errorf("oracle contains invalid version %q", ref.Version)
		}
		key := powerKey{powerType: ref.Type, name: ref.Name}
		if current, found := versions[key]; !found || parsed.GreaterThan(current) {
			versions[key] = parsed
			latest[key] = ref
		}
	}
	result := make([]domain.PowerRef, 0, len(latest))
	for _, ref := range latest {
		result = append(result, ref)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Type != result[right].Type {
			return result[left].Type < result[right].Type
		}
		return result[left].Name < result[right].Name
	})
	return result, nil
}

func testCanonicalPowerOracleMutations(t *testing.T) {
	t.Helper()
	for _, mutation := range []struct {
		name  string
		index int
	}{
		{name: "omitted_ref", index: 7},
		{name: "omitted_highest_version", index: 3},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			mutated := append([]domain.PowerRef(nil), canonicalPowerOracle[:mutation.index]...)
			mutated = append(mutated, canonicalPowerOracle[mutation.index+1:]...)
			if err := compareRefsToOracle(mutated); err == nil {
				t.Fatal("oracle comparison accepted an omitted committed fixture")
			}
			if mutation.name == "omitted_highest_version" {
				want, err := latestPowerRefs(canonicalPowerOracle)
				if err != nil {
					t.Fatal(err)
				}
				got, err := latestPowerRefs(mutated)
				if err != nil {
					t.Fatal(err)
				}
				if reflect.DeepEqual(got, want) {
					t.Fatal("independent SemVer maximum accepted an omitted highest version")
				}
			}
		})
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

type contractOperation struct {
	name           string
	ref            domain.PowerRef
	hasDestination bool
	invoke         func(context.Context, cloud.Client, domain.PowerRef, string) (domain.MCPServer, error)
}

func assertClientOperationErrors(t *testing.T, client cloud.Client) {
	t.Helper()
	cancellationOperations := []contractOperation{
		{name: "List", invoke: func(ctx context.Context, client cloud.Client, _ domain.PowerRef, _ string) (domain.MCPServer, error) {
			_, err := client.List(ctx)
			return domain.MCPServer{}, err
		}},
		{name: "Resolve", ref: oracleRef(t, domain.PowerTypeAgent), invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, _ string) (domain.MCPServer, error) {
			_, err := client.Resolve(ctx, ref)
			return domain.MCPServer{}, err
		}},
		{name: "DownloadSkill", ref: oracleRef(t, domain.PowerTypeSkill), hasDestination: true, invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, destination string) (domain.MCPServer, error) {
			return domain.MCPServer{}, client.DownloadSkill(ctx, ref, destination)
		}},
		{name: "DownloadPlugin", ref: oracleRef(t, domain.PowerTypePlugin), hasDestination: true, invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, destination string) (domain.MCPServer, error) {
			return domain.MCPServer{}, client.DownloadPlugin(ctx, ref, destination)
		}},
		{name: "GetMCP", ref: oracleRef(t, domain.PowerTypeMCP), invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, _ string) (domain.MCPServer, error) {
			return client.GetMCP(ctx, ref)
		}},
		{name: "DownloadAgent", ref: oracleRef(t, domain.PowerTypeAgent), hasDestination: true, invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, destination string) (domain.MCPServer, error) {
			return domain.MCPServer{}, client.DownloadAgent(ctx, ref, destination)
		}},
	}
	for _, operation := range cancellationOperations {
		t.Run("pre_cancel/"+operation.name, func(t *testing.T) {
			root := privateTestDir(t)
			destination := filepath.Join(root, "destination")
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			server, err := operation.invoke(cancelled, client, operation.ref, destination)
			assertExclusiveContractError(t, err, context.Canceled)
			if !reflect.DeepEqual(server, domain.MCPServer{}) {
				t.Fatalf("%s(cancelled) MCP result = %#v, want zero", operation.name, server)
			}
			if operation.hasDestination {
				assertNoDownloadResidue(t, root, destination)
			}
		})
	}

	missingByType := make(map[domain.PowerType]domain.PowerRef)
	for _, powerType := range []domain.PowerType{
		domain.PowerTypeSkill, domain.PowerTypeMCP, domain.PowerTypePlugin, domain.PowerTypeAgent,
	} {
		missingByType[powerType] = domain.PowerRef{Type: powerType, Name: "contract-exact-miss", Version: "v9.9.9"}
	}
	missOperations := []contractOperation{
		{name: "Resolve/skill", ref: missingByType[domain.PowerTypeSkill], invoke: resolveOperation},
		{name: "Resolve/mcp", ref: missingByType[domain.PowerTypeMCP], invoke: resolveOperation},
		{name: "Resolve/plugin", ref: missingByType[domain.PowerTypePlugin], invoke: resolveOperation},
		{name: "Resolve/agent", ref: missingByType[domain.PowerTypeAgent], invoke: resolveOperation},
		{name: "DownloadSkill", ref: missingByType[domain.PowerTypeSkill], hasDestination: true, invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, destination string) (domain.MCPServer, error) {
			return domain.MCPServer{}, client.DownloadSkill(ctx, ref, destination)
		}},
		{name: "DownloadPlugin", ref: missingByType[domain.PowerTypePlugin], hasDestination: true, invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, destination string) (domain.MCPServer, error) {
			return domain.MCPServer{}, client.DownloadPlugin(ctx, ref, destination)
		}},
		{name: "GetMCP", ref: missingByType[domain.PowerTypeMCP], invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, _ string) (domain.MCPServer, error) {
			return client.GetMCP(ctx, ref)
		}},
		{name: "DownloadAgent", ref: missingByType[domain.PowerTypeAgent], hasDestination: true, invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, destination string) (domain.MCPServer, error) {
			return domain.MCPServer{}, client.DownloadAgent(ctx, ref, destination)
		}},
	}
	for _, operation := range missOperations {
		t.Run("exact_miss/"+operation.name, func(t *testing.T) {
			root := privateTestDir(t)
			destination := filepath.Join(root, "destination")
			server, err := operation.invoke(context.Background(), client, operation.ref, destination)
			assertExclusiveContractError(t, err, cloud.ErrNotFound)
			if !reflect.DeepEqual(server, domain.MCPServer{}) {
				t.Fatalf("%s(exact miss) MCP result = %#v, want zero", operation.name, server)
			}
			if operation.hasDestination {
				assertNoDownloadResidue(t, root, destination)
			}
		})
	}

	for _, invalidName := range []string{"Bad_Name", "a--b", strings.Repeat("a", 65)} {
		for _, operation := range []contractOperation{
			{name: "Resolve", ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: invalidName}, invoke: resolveOperation},
			{name: "DownloadSkill", ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: invalidName, Version: "v1.0.0"}, hasDestination: true, invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, destination string) (domain.MCPServer, error) {
				return domain.MCPServer{}, client.DownloadSkill(ctx, ref, destination)
			}},
			{name: "GetMCP", ref: domain.PowerRef{Type: domain.PowerTypeMCP, Name: invalidName, Version: "v1.0.0"}, invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, _ string) (domain.MCPServer, error) {
				return client.GetMCP(ctx, ref)
			}},
			{name: "DownloadAgent", ref: domain.PowerRef{Type: domain.PowerTypeAgent, Name: invalidName, Version: "v1.0.0"}, hasDestination: true, invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, destination string) (domain.MCPServer, error) {
				return domain.MCPServer{}, client.DownloadAgent(ctx, ref, destination)
			}},
		} {
			t.Run("invalid_name/"+operation.name+"/"+invalidName, func(t *testing.T) {
				root := privateTestDir(t)
				destination := filepath.Join(root, "destination")
				server, err := operation.invoke(context.Background(), client, operation.ref, destination)
				assertExclusiveContractError(t, err, cloud.ErrNotFound)
				if !reflect.DeepEqual(server, domain.MCPServer{}) {
					t.Fatalf("%s(invalid name) MCP result = %#v, want zero", operation.name, server)
				}
				if operation.hasDestination {
					assertNoDownloadResidue(t, root, destination)
				}
			})
		}
	}
}

func resolveOperation(ctx context.Context, client cloud.Client, ref domain.PowerRef, _ string) (domain.MCPServer, error) {
	_, err := client.Resolve(ctx, ref)
	return domain.MCPServer{}, err
}

func oracleRef(t *testing.T, powerType domain.PowerType) domain.PowerRef {
	t.Helper()
	for _, ref := range canonicalPowerOracle {
		if ref.Type == powerType {
			return ref
		}
	}
	t.Fatalf("oracle has no %s fixture", powerType)
	return domain.PowerRef{}
}

func assertExclusiveContractError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
	rejected := []error{
		context.Canceled, context.DeadlineExceeded,
		cloud.ErrNotFound, cloud.ErrYanked, cloud.ErrInvalidResponse,
		cloud.ErrIntegrity, cloud.ErrUnsafeContent, cloud.ErrProviderConfig,
	}
	for _, sentinel := range rejected {
		if sentinel != want && errors.Is(got, sentinel) {
			t.Fatalf("error = %v, want only %v and explicitly reject %v", got, want, sentinel)
		}
	}
}

func assertNoDownloadResidue(t *testing.T, root, destination string) {
	t.Helper()
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("download destination %q exists or cannot be inspected after failure: %v", destination, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("download failure left residue below %q: %v", root, entries)
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
	if err := compareRefsToOracle(localRefs); err != nil {
		t.Fatalf("Local List oracle mismatch: %v", err)
	}
	if err := compareRefsToOracle(remoteRefs); err != nil {
		t.Fatalf("Remote List oracle mismatch: %v", err)
	}

	for _, ref := range canonicalPowerOracle {
		localExact, localErr := localClient.Resolve(context.Background(), ref)
		remoteExact, remoteErr := remoteClient.Resolve(context.Background(), ref)
		if localErr != nil || remoteErr != nil || localExact != remoteExact {
			t.Fatalf("exact Resolve parity for %+v: local=%+v/%v remote=%+v/%v", ref, localExact, localErr, remoteExact, remoteErr)
		}
		comparePowerOutcome(t, localClient, remoteClient, ref)
	}
	latest, err := latestPowerRefs(canonicalPowerOracle)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range latest {
		query := domain.PowerRef{Type: want.Type, Name: want.Name}
		localLatest, localErr := localClient.Resolve(context.Background(), query)
		remoteLatest, remoteErr := remoteClient.Resolve(context.Background(), query)
		if localErr != nil || remoteErr != nil || localLatest != want || remoteLatest != want {
			t.Fatalf("latest Resolve oracle for %+v: local=%+v/%v remote=%+v/%v want=%+v", query, localLatest, localErr, remoteLatest, remoteErr, want)
		}
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
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		node := normalizedNode{Path: filepath.ToSlash(relative), Mode: info.Mode().Perm()}
		if entry.IsDir() {
		} else {
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
	return normalizedNode{Path: filepath.ToSlash(logicalPath), Mode: info.Mode().Perm(), Bytes: string(contents)}
}

func privateTestDir(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return directory
}

// TestRemoteSubmissionAndMCPContract 通过真实 Remote Client、fake APC 与生命周期服务，
// 复验四类资源的提交身份、本人提交查询、活动配额错误以及完整 MCP 资源解包边界。
func TestRemoteSubmissionAndMCPContract(t *testing.T) {
	root, err := filepath.Abs("resources")
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot := t.TempDir()
	skillRoot := filepath.Join(sourceRoot, "contract-skill")
	writeContractSourceFile(t, filepath.Join(skillRoot, "SKILL.md"), "---\nname: contract-skill\nversion: \"v1.2.3\"\ndescription: Cross-layer Skill\n---\n# Contract Skill\n")
	writeContractSourceFile(t, filepath.Join(skillRoot, "skill-card.md"), contractSkillCard)
	pluginRoot := filepath.Join(sourceRoot, "contract-plugin")
	writeContractSourceFile(t, filepath.Join(pluginRoot, "apc-plugin.json"), `{"schema_version":1,"name":"contract-plugin","version":"v2.0.0","description":"Cross-layer Plugin","skills":["skills/helper"]}`)
	writeContractSourceFile(t, filepath.Join(pluginRoot, "skills", "helper", "SKILL.md"), "---\nname: helper\nversion: \"v1.0.0\"\ndescription: Embedded helper\n---\n# Helper\n")
	writeContractSourceFile(t, filepath.Join(pluginRoot, "skills", "helper", "skill-card.md"), contractSkillCard)
	server := newCanonicalRemoteServer(t, root)
	client, err := remote.NewWithAPIKey(server.server.URL, &http.Client{Timeout: 5 * time.Second}, canonicalSubmissionAPIKey)
	if err != nil {
		t.Fatal(err)
	}
	submitService, err := lifecycle.NewSubmit(client)
	if err != nil {
		t.Fatal(err)
	}

	submissions := []struct {
		powerType   domain.PowerType
		path        string
		name        string
		version     string
		description string
		contentType string
	}{
		{
			powerType: domain.PowerTypeSkill,
			path:      skillRoot,
			name:      "contract-skill", version: "v1.2.3",
			description: "Cross-layer Skill", contentType: "application/gzip",
		},
		{
			powerType: domain.PowerTypePlugin,
			path:      pluginRoot,
			name:      "contract-plugin", version: "v2.0.0",
			description: "Cross-layer Plugin", contentType: "application/gzip",
		},
		{
			powerType: domain.PowerTypeMCP,
			path:      filepath.Join(root, "mcps", "filesystem", "v0.1.0", "MCP.json"),
			name:      "filesystem", version: "v0.1.0",
			description: "Filesystem MCP server with a workspace root", contentType: "application/json",
		},
		{
			powerType: domain.PowerTypeAgent,
			path:      filepath.Join(root, "agents", "bianbu-agent-default", "v1.0.0", "agent.yaml"),
			name:      "bianbu-agent-default", version: "v1.0.0",
			description: "Project-local Bianbu agent", contentType: "application/yaml",
		},
	}
	for _, test := range submissions {
		t.Run("submit_"+string(test.powerType), func(t *testing.T) {
			message := "publish " + string(test.powerType)
			result, err := submitService.Submit(context.Background(), cli.Flags{
				Type: string(test.powerType), Message: "  " + message + "  ",
			}, []string{test.path})
			if err != nil {
				t.Fatalf("Submit() error = %v", err)
			}
			if result.Outcome != string(domain.SubmissionAccepted) || result.SubmissionID != "sub_"+string(test.powerType) || result.Status != string(domain.SubmissionReceived) {
				t.Fatalf("Submit() = %#v, want accepted/received", result)
			}
			server.assertReceivedSubmission(t, test.powerType, test.name, test.version, test.description, message, test.contentType)
		})
	}

	queryService, err := lifecycle.NewSubmissionQuery(client)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := queryService.List(context.Background(), cli.Flags{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed.Summaries) != 1 || listed.Summaries[0] != (domain.SubmissionSummary{
		SubmissionID: "sub_active", Type: domain.PowerTypeSkill, Name: "contract-skill", Version: "v1.2.3",
		Status: domain.SubmissionReviewPending, CreatedAt: "2026-08-25T10:00:00Z",
	}) {
		t.Fatalf("List() = %#v, want one active contract-skill submission", listed)
	}
	detail, err := queryService.Get(context.Background(), "sub_active")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	wantDetail := domain.Submission{
		SubmissionID: "sub_active", Type: domain.PowerTypeSkill, Name: "contract-skill", Version: "v1.2.3",
		Description: "Cross-layer Skill", Message: "publish skill", Status: domain.SubmissionReviewPending,
		CreatedAt: "2026-08-25T10:00:00Z", UpdatedAt: "2026-08-25T10:01:00Z", ContentAvailable: true,
		ValidationErrors: []string{}, ReviewComment: "", PublishedVersion: "",
		Timeline: []domain.SubmissionEvent{
			{
				EventID: "evt_received", EventType: "status_changed", StatusFrom: "", StatusTo: domain.SubmissionReceived,
				Timestamp: "2026-08-25T10:00:00Z", Message: "submission received",
			},
			{
				EventID: "evt_review", EventType: "status_changed", StatusFrom: domain.SubmissionReceived, StatusTo: domain.SubmissionReviewPending,
				Timestamp: "2026-08-25T10:01:00Z", Message: "review requested",
			},
		},
	}
	if detail.Detail == nil || !reflect.DeepEqual(*detail.Detail, wantDetail) {
		t.Fatalf("Get() = %#v, want full detail %#v", detail, wantDetail)
	}

	server.mu.Lock()
	server.tooManyActive = true
	server.mu.Unlock()
	_, err = submitService.Submit(context.Background(), cli.Flags{
		Type: "agent", Message: "quota probe",
	}, []string{filepath.Join(root, "agents", "bianbu-agent-default", "v1.0.0", "agent.yaml")})
	if !errors.Is(err, cloud.ErrTooManyActiveSubmissions) || !errors.Is(err, cloud.ErrSubmissionConflict) ||
		!strings.Contains(err.Error(), "TOO_MANY_ACTIVE_SUBMISSIONS") {
		t.Fatalf("Submit() quota error = %v, want typed TOO_MANY_ACTIVE_SUBMISSIONS conflict", err)
	}

	mcp, err := client.GetMCP(context.Background(), domain.PowerRef{
		Type: domain.PowerTypeMCP, Name: "filesystem", Version: "v0.1.0",
	})
	if err != nil {
		t.Fatalf("GetMCP() error = %v", err)
	}
	wantMCP := domain.MCPServer{
		Transport: domain.MCPTransportStdio,
		Command:   "npx",
		Args:      []string{"-y", "@modelcontextprotocol/server-filesystem", "/workspace"},
	}
	if !reflect.DeepEqual(mcp, wantMCP) {
		t.Fatalf("GetMCP() = %#v, want only definition %#v", mcp, wantMCP)
	}
}

const contractSkillCard = `# Skill Card: Contract Fixture

## Capability
Cross-layer contract verification.

## Use Cases
Exercise submission composition.

## Inputs and Outputs
Input source files and output a submission.

## Dependencies
None.

## Limitations
Test fixture only.

## Maintainer
Owner: APM tests
Contact: tests@example.com
`

func writeContractSourceFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBundledMCPResourcesUseCanonicalV1Schema(t *testing.T) {
	root := filepath.Join("resources", "mcps")
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "MCP.json" {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if len(parts) != 3 || parts[2] != "MCP.json" {
			return fmt.Errorf("bundled MCP path %q is not <name>/<version>/MCP.json", relative)
		}
		resource, err := domain.DecodeMCPResourceJSON(readCanonicalFile(t, path))
		if err != nil {
			return fmt.Errorf("decode bundled MCP %q: %w", relative, err)
		}
		if resource.Name != parts[0] || resource.Version != parts[1] {
			return fmt.Errorf("bundled MCP %q declares %s@%s", relative, resource.Name, resource.Version)
		}
		count++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("bundled MCP fixture walk found no MCP.json files")
	}
}
