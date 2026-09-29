package cloud_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"apm/internal/catalog"
	"apm/internal/cli"
	"apm/internal/cloud"
	"apm/internal/cloud/local"
	"apm/internal/cloud/remote"
	"apm/internal/domain"
	"apm/internal/lifecycle"
	"apm/internal/pluginartifact"
	"apm/internal/submission"
	"go.yaml.in/yaml/v3"
)

type clientFactory struct {
	name        string
	open        func(t *testing.T) cloud.Client
	wantCatalog []byte
}

var canonicalPowerOracle = []domain.PowerRef{
	{Name: "brief-status", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	{Name: "checklist-first", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	{Name: "code-comment-cn", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	{Name: "commit-message-helper", Type: domain.PowerTypeSkill, Version: "v0.1.0"},
	{Name: "commit-message-helper", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	{Name: "error-context", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	{Name: "foo-before-summary", Type: domain.PowerTypeSkill, Version: "v0.1.0"},
	{Name: "foo-before-summary", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	{Name: "meeting-notes", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	{Name: "plain-language", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	{Name: "test-case-namer", Type: domain.PowerTypeSkill, Version: "v0.1.0"},
	{Name: "test-case-namer", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	{Name: "worklog-entry", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"},
	{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.0.1"},
	{Name: "company-tools", Type: domain.PowerTypeMCP, Version: "v0.0.1"},
	{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v0.1.0"},
	{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v0.0.1"},
	{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"},
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

func TestCatalogSourceContract(t *testing.T) {
	factories, _ := providerFactories(t)
	for _, factory := range factories {
		t.Run(factory.name, func(t *testing.T) {
			client := factory.open(t)
			source, ok := client.(cloud.CatalogSource)
			if !ok {
				t.Fatalf("%T does not implement cloud.CatalogSource", client)
			}
			runCatalogSourceContract(t, source, factory.wantCatalog)
		})
	}
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
	entry := contractRemoteEntry(remoteFixture.resources[0])
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
			err := provider.client.DownloadSkill(context.Background(), entry, destination)
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
		{name: "local", wantCatalog: canonicalLocalCatalog(t, root), open: func(t *testing.T) cloud.Client {
			client, err := local.New(root)
			if err != nil {
				t.Fatal(err)
			}
			return client
		}},
		{name: "remote", wantCatalog: canonicalRemoteCatalog(t, server.resources), open: func(t *testing.T) cloud.Client {
			client, err := remote.NewWithAPIKey(server.server.URL, &http.Client{Timeout: 5 * time.Second}, canonicalSubmissionAPIKey)
			if err != nil {
				t.Fatal(err)
			}
			return client
		}},
	}, trustedTemp
}

func runCatalogSourceContract(t *testing.T, source cloud.CatalogSource, wantCatalog []byte) {
	t.Helper()
	wantDigest := sha256.Sum256(wantCatalog)
	wantVersion := domain.CatalogVersion{SHA256: hex.EncodeToString(wantDigest[:]), Size: int64(len(wantCatalog))}

	for call := 0; call < 2; call++ {
		got, err := source.CatalogVersion(context.Background())
		if err != nil || got != wantVersion {
			t.Fatalf("CatalogVersion() call %d = %+v, %v; want %+v", call+1, got, err, wantVersion)
		}
	}
	for call := 0; call < 2; call++ {
		destination := filepath.Join(privateTestDir(t), "catalog.json")
		if err := source.DownloadCatalog(context.Background(), destination); err != nil {
			t.Fatalf("DownloadCatalog() call %d error = %v", call+1, err)
		}
		got, err := os.ReadFile(destination)
		if err != nil || !reflect.DeepEqual(got, wantCatalog) {
			t.Fatalf("DownloadCatalog() call %d bytes = %q, %v; want exact %q", call+1, got, err, wantCatalog)
		}
		info, err := os.Stat(destination)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("DownloadCatalog() call %d mode = %v, %v; want 0600", call+1, info, err)
		}
	}

	preexisting := filepath.Join(privateTestDir(t), "catalog.json")
	if err := os.WriteFile(preexisting, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := source.DownloadCatalog(context.Background(), preexisting); !errors.Is(err, cloud.ErrCatalogDestination) {
		t.Fatalf("DownloadCatalog(preexisting) error = %v, want ErrCatalogDestination", err)
	}
	if got, err := os.ReadFile(preexisting); err != nil || string(got) != "sentinel" {
		t.Fatalf("preexisting destination = %q, %v; want unchanged sentinel", got, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.CatalogVersion(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled CatalogVersion() error = %v, want context.Canceled", err)
	}
	canceledDestination := filepath.Join(privateTestDir(t), "catalog.json")
	if err := source.DownloadCatalog(ctx, canceledDestination); !errors.Is(err, context.Canceled) || errors.Is(err, cloud.ErrCatalogDestination) {
		t.Fatalf("canceled DownloadCatalog() error = %v, want only context.Canceled", err)
	}
	if _, err := os.Lstat(canceledDestination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled destination stat error = %v, want absent", err)
	}
}

func canonicalLocalCatalog(t *testing.T, root string) []byte {
	t.Helper()
	entries := make([]domain.CatalogEntry, 0, len(canonicalPowerOracle))
	for _, ref := range canonicalPowerOracle {
		metadata := canonicalResourceMetadata(t, root, ref)
		content, err := canonicalResourceContent(t, root, ref)
		if err != nil {
			t.Fatalf("build canonical Local resource %+v: %v", ref, err)
		}
		digest := md5.Sum(content)
		entries = append(entries, domain.CatalogEntry{
			Ref: ref, Description: metadata.description, Message: "local fixture",
			ContentType: metadata.contentType, ContentSize: int64(len(content)), ContentMD5: hex.EncodeToString(digest[:]),
			PublishedAt: "1970-01-01T00:00:00Z", Tags: []string{},
			NameZH: metadata.nameZH, NameEN: metadata.nameEN, DescriptionZH: metadata.descriptionZH,
		})
	}
	contents, err := catalog.Encode(entries)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

// canonicalMetadata 是一类资源权威文件解析出的发布元数据，作为 provider oracle 的
// 独立期望值：直接读 fixture 文件，不调用被测解析代码。
type canonicalMetadata struct {
	description   string
	contentType   string
	nameZH        string
	nameEN        string
	descriptionZH string
}

// canonicalResourceMetadata 从四类资源的唯一权威文件读出 description 与三个 v2 展示字段：
// Skill 的 SKILL.md frontmatter、Plugin 的 apc-plugin.json、MCP 的 MCP.json、Agent 的 agent.yaml。
func canonicalResourceMetadata(t *testing.T, root string, ref domain.PowerRef) canonicalMetadata {
	t.Helper()
	switch ref.Type {
	case domain.PowerTypeSkill:
		display := canonicalSkillDisplayMetadata(t, filepath.Join(root, "skills", ref.Name, ref.Version, "SKILL.md"))
		return canonicalMetadata{
			description: display.description, contentType: "application/gzip",
			nameZH: display.nameZH, nameEN: display.nameEN, descriptionZH: display.descriptionZH,
		}
	case domain.PowerTypePlugin:
		var manifest pluginartifact.APCPluginManifest
		manifestBytes := readCanonicalFile(t, filepath.Join(root, "plugins", ref.Name, ref.Version, "apc-plugin.json"))
		if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
			t.Fatal(err)
		}
		return canonicalMetadata{
			description: manifest.Description, contentType: "application/gzip",
			nameZH: manifest.NameZH, nameEN: manifest.NameEN, descriptionZH: manifest.DescriptionZH,
		}
	case domain.PowerTypeMCP:
		raw := readCanonicalFile(t, filepath.Join(root, "mcps", ref.Name, ref.Version, "MCP.json"))
		resource, err := domain.DecodeMCPResourceJSON(raw)
		if err != nil {
			t.Fatal(err)
		}
		return canonicalMetadata{
			description: resource.Description, contentType: "application/json",
			nameZH: resource.NameZH, nameEN: resource.NameEN, descriptionZH: resource.DescriptionZH,
		}
	case domain.PowerTypeAgent:
		config, err := domain.DecodeAgentConfigYAML(readCanonicalFile(t, filepath.Join(root, "agents", ref.Name, ref.Version, "agent.yaml")))
		if err != nil {
			t.Fatal(err)
		}
		return canonicalMetadata{
			description: config.Agent.Description, contentType: "application/gzip",
			nameZH: config.Agent.NameZH, nameEN: config.Agent.NameEN, descriptionZH: config.Agent.DescriptionZH,
		}
	default:
		t.Fatalf("unsupported canonical resource type %q", ref.Type)
		return canonicalMetadata{}
	}
}

func canonicalResourceContent(t *testing.T, root string, ref domain.PowerRef) ([]byte, error) {
	t.Helper()
	switch ref.Type {
	case domain.PowerTypeSkill:
		return submission.BuildTarGz(filepath.Join(root, "skills", ref.Name, ref.Version))
	case domain.PowerTypePlugin:
		return submission.BuildTarGz(filepath.Join(root, "plugins", ref.Name, ref.Version))
	case domain.PowerTypeMCP:
		raw := readCanonicalFile(t, filepath.Join(root, "mcps", ref.Name, ref.Version, "MCP.json"))
		return submission.CanonicalizeJSON(raw)
	case domain.PowerTypeAgent:
		return submission.BuildTarGz(filepath.Join(root, "agents", ref.Name, ref.Version))
	default:
		t.Fatalf("unsupported canonical resource type %q", ref.Type)
		return nil, nil
	}
}

type canonicalSkillMetadata struct {
	description   string
	nameZH        string
	nameEN        string
	descriptionZH string
}

func canonicalSkillDisplayMetadata(t *testing.T, path string) canonicalSkillMetadata {
	t.Helper()
	raw := readCanonicalFile(t, path)
	if !bytes.HasPrefix(raw, []byte("---\n")) {
		t.Fatalf("canonical Skill %q has no frontmatter", path)
	}
	end := bytes.Index(raw[len("---\n"):], []byte("\n---"))
	if end < 0 {
		t.Fatalf("canonical Skill %q has no closing frontmatter", path)
	}
	var metadata struct {
		Description   string `yaml:"description"`
		NameZH        string `yaml:"name_zh"`
		NameEN        string `yaml:"name_en"`
		DescriptionZH string `yaml:"description_zh"`
	}
	if err := yaml.Unmarshal(raw[len("---\n"):len("---\n")+end], &metadata); err != nil {
		t.Fatal(err)
	}
	return canonicalSkillMetadata{
		description:   metadata.Description,
		nameZH:        metadata.NameZH,
		nameEN:        metadata.NameEN,
		descriptionZH: metadata.DescriptionZH,
	}
}

func canonicalRemoteCatalog(t *testing.T, resources []canonicalRemoteResource) []byte {
	t.Helper()
	entries := make([]domain.CatalogEntry, 0, len(resources))
	for _, resource := range resources {
		digest := md5.Sum(resource.content)
		entries = append(entries, domain.CatalogEntry{
			Ref: resource.ref, Description: "canonical test fixture", Message: "publish canonical fixture",
			ContentType: resource.contentType, ContentSize: int64(len(resource.content)), ContentMD5: hex.EncodeToString(digest[:]),
			PublishedAt: canonicalSnapshotTime, Tags: []string{},
		})
	}
	contents, err := catalog.Encode(entries)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func catalogSnapshotForClient(t *testing.T, client cloud.Client) catalog.Snapshot {
	t.Helper()
	destination := filepath.Join(privateTestDir(t), "catalog.json")
	if err := client.DownloadCatalog(context.Background(), destination); err != nil {
		t.Fatalf("DownloadCatalog() for content contract: %v", err)
	}
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Decode(contents)
	if err != nil {
		t.Fatalf("decode provider catalog for content contract: %v", err)
	}
	return snapshot
}

func contractEntry(ref domain.PowerRef) domain.CatalogEntry {
	contentType := map[domain.PowerType]string{
		domain.PowerTypeSkill:  "application/gzip",
		domain.PowerTypePlugin: "application/gzip",
		domain.PowerTypeMCP:    "application/json",
		domain.PowerTypeAgent:  "application/gzip",
	}[ref.Type]
	return domain.CatalogEntry{
		Ref: ref, Description: "contract fixture", Message: "contract fixture",
		ContentType: contentType, ContentSize: 1, ContentMD5: strings.Repeat("0", 32),
		PublishedAt: canonicalSnapshotTime, Tags: []string{},
	}
}

func contractRemoteEntry(resource canonicalRemoteResource) domain.CatalogEntry {
	entry := contractEntry(resource.ref)
	digest := md5.Sum(resource.content)
	entry.Description = "canonical test fixture"
	entry.Message = "publish canonical fixture"
	entry.ContentType = resource.contentType
	entry.ContentSize = int64(len(resource.content))
	entry.ContentMD5 = hex.EncodeToString(digest[:])
	return entry
}

func runProviderContract(t *testing.T, client cloud.Client) {
	t.Helper()
	snapshot := catalogSnapshotForClient(t, client)
	entries := snapshot.List("")
	refs := make([]domain.PowerRef, len(entries))
	for index, entry := range entries {
		refs[index] = entry.Ref
	}
	if err := compareRefsToOracle(refs); err != nil {
		t.Fatalf("catalog snapshot does not match the committed fixture oracle: %v", err)
	}

	for _, ref := range canonicalPowerOracle {
		entry, err := snapshot.Metadata(ref)
		if err != nil {
			t.Fatalf("Catalog Metadata(%+v): %v", ref, err)
		}
		assertPowerReadable(t, client, entry)
		assertMismatchedCatalogMetadataRejected(t, client, entry)
		assertInvalidCatalogMetadataRejected(t, client, entry)
	}
	assertClientOperationErrors(t, client)
}

func compareRefsToOracle(got []domain.PowerRef) error {
	if !reflect.DeepEqual(got, canonicalPowerOracle) {
		return fmt.Errorf("got %#v, want %#v", got, canonicalPowerOracle)
	}
	return nil
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
		})
	}
}

func assertPowerReadable(t *testing.T, client cloud.Client, entry domain.CatalogEntry) {
	t.Helper()
	ref := entry.Ref
	switch ref.Type {
	case domain.PowerTypeSkill:
		if err := client.DownloadSkill(context.Background(), entry, filepath.Join(privateTestDir(t), "skill")); err != nil {
			t.Fatalf("DownloadSkill(%+v): %v", ref, err)
		}
	case domain.PowerTypePlugin:
		if err := client.DownloadPlugin(context.Background(), entry, filepath.Join(privateTestDir(t), "plugin")); err != nil {
			t.Fatalf("DownloadPlugin(%+v): %v", ref, err)
		}
	case domain.PowerTypeMCP:
		if _, err := client.GetMCP(context.Background(), entry); err != nil {
			t.Fatalf("GetMCP(%+v): %v", ref, err)
		}
	case domain.PowerTypeAgent:
		if err := client.DownloadAgent(context.Background(), entry, filepath.Join(privateTestDir(t), "agent")); err != nil {
			t.Fatalf("DownloadAgent(%+v): %v", ref, err)
		}
	default:
		t.Fatalf("unsupported Power type %q", ref.Type)
	}
}

func assertMismatchedCatalogMetadataRejected(t *testing.T, client cloud.Client, entry domain.CatalogEntry) {
	t.Helper()
	entry.ContentSize++
	var err error
	switch entry.Ref.Type {
	case domain.PowerTypeSkill:
		err = client.DownloadSkill(context.Background(), entry, filepath.Join(privateTestDir(t), "skill"))
	case domain.PowerTypePlugin:
		err = client.DownloadPlugin(context.Background(), entry, filepath.Join(privateTestDir(t), "plugin"))
	case domain.PowerTypeMCP:
		_, err = client.GetMCP(context.Background(), entry)
	case domain.PowerTypeAgent:
		err = client.DownloadAgent(context.Background(), entry, filepath.Join(privateTestDir(t), "agent"))
	default:
		t.Fatalf("unsupported Power type %q", entry.Ref.Type)
	}
	if !errors.Is(err, cloud.ErrIntegrity) {
		t.Fatalf("content with mismatched catalog size for %+v error = %v, want ErrIntegrity", entry.Ref, err)
	}
}

func assertInvalidCatalogMetadataRejected(t *testing.T, client cloud.Client, entry domain.CatalogEntry) {
	t.Helper()
	entry.Tags = nil
	var err error
	switch entry.Ref.Type {
	case domain.PowerTypeSkill:
		err = client.DownloadSkill(context.Background(), entry, filepath.Join(privateTestDir(t), "skill"))
	case domain.PowerTypePlugin:
		err = client.DownloadPlugin(context.Background(), entry, filepath.Join(privateTestDir(t), "plugin"))
	case domain.PowerTypeMCP:
		_, err = client.GetMCP(context.Background(), entry)
	case domain.PowerTypeAgent:
		err = client.DownloadAgent(context.Background(), entry, filepath.Join(privateTestDir(t), "agent"))
	default:
		t.Fatalf("unsupported Power type %q", entry.Ref.Type)
	}
	assertExclusiveContractError(t, err, cloud.ErrInvalidResponse)
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
		{name: "DownloadSkill", ref: oracleRef(t, domain.PowerTypeSkill), hasDestination: true, invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, destination string) (domain.MCPServer, error) {
			return domain.MCPServer{}, client.DownloadSkill(ctx, contractEntry(ref), destination)
		}},
		{name: "DownloadPlugin", ref: oracleRef(t, domain.PowerTypePlugin), hasDestination: true, invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, destination string) (domain.MCPServer, error) {
			return domain.MCPServer{}, client.DownloadPlugin(ctx, contractEntry(ref), destination)
		}},
		{name: "GetMCP", ref: oracleRef(t, domain.PowerTypeMCP), invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, _ string) (domain.MCPServer, error) {
			return client.GetMCP(ctx, contractEntry(ref))
		}},
		{name: "DownloadAgent", ref: oracleRef(t, domain.PowerTypeAgent), hasDestination: true, invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, destination string) (domain.MCPServer, error) {
			return domain.MCPServer{}, client.DownloadAgent(ctx, contractEntry(ref), destination)
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
		{name: "DownloadSkill", ref: missingByType[domain.PowerTypeSkill], hasDestination: true, invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, destination string) (domain.MCPServer, error) {
			return domain.MCPServer{}, client.DownloadSkill(ctx, contractEntry(ref), destination)
		}},
		{name: "DownloadPlugin", ref: missingByType[domain.PowerTypePlugin], hasDestination: true, invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, destination string) (domain.MCPServer, error) {
			return domain.MCPServer{}, client.DownloadPlugin(ctx, contractEntry(ref), destination)
		}},
		{name: "GetMCP", ref: missingByType[domain.PowerTypeMCP], invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, _ string) (domain.MCPServer, error) {
			return client.GetMCP(ctx, contractEntry(ref))
		}},
		{name: "DownloadAgent", ref: missingByType[domain.PowerTypeAgent], hasDestination: true, invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, destination string) (domain.MCPServer, error) {
			return domain.MCPServer{}, client.DownloadAgent(ctx, contractEntry(ref), destination)
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
	for _, powerType := range []domain.PowerType{
		domain.PowerTypeSkill, domain.PowerTypeMCP, domain.PowerTypePlugin, domain.PowerTypeAgent,
	} {
		ref := missingByType[powerType]
		t.Run("exact_miss_invalid_metadata/"+string(powerType), func(t *testing.T) {
			root := privateTestDir(t)
			destination := filepath.Join(root, "destination")
			entry := contractEntry(ref)
			entry.Tags = nil
			server, err := invokeContentEntry(context.Background(), client, entry, destination)
			assertExclusiveContractError(t, err, cloud.ErrInvalidResponse)
			if !reflect.DeepEqual(server, domain.MCPServer{}) {
				t.Fatalf("%s(exact miss with invalid metadata) MCP result = %#v, want zero", powerType, server)
			}
			if powerType != domain.PowerTypeMCP {
				assertNoDownloadResidue(t, root, destination)
			}
		})
	}

	for _, invalidName := range []string{"Bad_Name", "a--b", strings.Repeat("a", 65)} {
		for _, operation := range []contractOperation{
			{name: "DownloadSkill", ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: invalidName, Version: "v1.0.0"}, hasDestination: true, invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, destination string) (domain.MCPServer, error) {
				return domain.MCPServer{}, client.DownloadSkill(ctx, contractEntry(ref), destination)
			}},
			{name: "GetMCP", ref: domain.PowerRef{Type: domain.PowerTypeMCP, Name: invalidName, Version: "v1.0.0"}, invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, _ string) (domain.MCPServer, error) {
				return client.GetMCP(ctx, contractEntry(ref))
			}},
			{name: "DownloadAgent", ref: domain.PowerRef{Type: domain.PowerTypeAgent, Name: invalidName, Version: "v1.0.0"}, hasDestination: true, invoke: func(ctx context.Context, client cloud.Client, ref domain.PowerRef, destination string) (domain.MCPServer, error) {
				return domain.MCPServer{}, client.DownloadAgent(ctx, contractEntry(ref), destination)
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

func invokeContentEntry(ctx context.Context, client cloud.Client, entry domain.CatalogEntry, destination string) (domain.MCPServer, error) {
	switch entry.Ref.Type {
	case domain.PowerTypeSkill:
		return domain.MCPServer{}, client.DownloadSkill(ctx, entry, destination)
	case domain.PowerTypePlugin:
		return domain.MCPServer{}, client.DownloadPlugin(ctx, entry, destination)
	case domain.PowerTypeMCP:
		return client.GetMCP(ctx, entry)
	case domain.PowerTypeAgent:
		return domain.MCPServer{}, client.DownloadAgent(ctx, entry, destination)
	default:
		return domain.MCPServer{}, fmt.Errorf("unsupported contract Power type %q", entry.Ref.Type)
	}
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
	localSnapshot := catalogSnapshotForClient(t, localClient)
	remoteSnapshot := catalogSnapshotForClient(t, remoteClient)
	localEntries := localSnapshot.List("")
	localRefs := make([]domain.PowerRef, len(localEntries))
	for index, entry := range localEntries {
		localRefs[index] = entry.Ref
	}
	remoteEntries := remoteSnapshot.List("")
	remoteRefs := make([]domain.PowerRef, len(remoteEntries))
	for index, entry := range remoteEntries {
		remoteRefs[index] = entry.Ref
	}
	if err := compareRefsToOracle(localRefs); err != nil {
		t.Fatalf("Local catalog oracle mismatch: %v", err)
	}
	if err := compareRefsToOracle(remoteRefs); err != nil {
		t.Fatalf("Remote catalog oracle mismatch: %v", err)
	}

	for _, ref := range canonicalPowerOracle {
		localEntry, localEntryErr := localSnapshot.Metadata(ref)
		remoteEntry, remoteEntryErr := remoteSnapshot.Metadata(ref)
		if localEntryErr != nil || remoteEntryErr != nil {
			t.Fatalf("catalog entry parity for %+v: local=%v remote=%v", ref, localEntryErr, remoteEntryErr)
		}
		comparePowerOutcome(t, localClient, remoteClient, localEntry, remoteEntry)
	}
}

func comparePowerOutcome(t *testing.T, localClient, remoteClient cloud.Client, localEntry, remoteEntry domain.CatalogEntry) {
	t.Helper()
	ref := localEntry.Ref
	if remoteEntry.Ref != ref {
		t.Fatalf("content entry identity parity: local=%+v remote=%+v", ref, remoteEntry.Ref)
	}
	switch ref.Type {
	case domain.PowerTypeSkill, domain.PowerTypePlugin:
		localTarget := filepath.Join(privateTestDir(t), "local")
		remoteTarget := filepath.Join(privateTestDir(t), "remote")
		var localErr, remoteErr error
		if ref.Type == domain.PowerTypeSkill {
			localErr = localClient.DownloadSkill(context.Background(), localEntry, localTarget)
			remoteErr = remoteClient.DownloadSkill(context.Background(), remoteEntry, remoteTarget)
		} else {
			localErr = localClient.DownloadPlugin(context.Background(), localEntry, localTarget)
			remoteErr = remoteClient.DownloadPlugin(context.Background(), remoteEntry, remoteTarget)
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
		localServer, localErr := localClient.GetMCP(context.Background(), localEntry)
		remoteServer, remoteErr := remoteClient.GetMCP(context.Background(), remoteEntry)
		if localErr != nil || remoteErr != nil || !reflect.DeepEqual(localServer, remoteServer) {
			t.Fatalf("MCP parity for %+v: local=%#v/%v remote=%#v/%v", ref, localServer, localErr, remoteServer, remoteErr)
		}
	case domain.PowerTypeAgent:
		localTarget := filepath.Join(privateTestDir(t), "local")
		remoteTarget := filepath.Join(privateTestDir(t), "remote")
		localErr := localClient.DownloadAgent(context.Background(), localEntry, localTarget)
		remoteErr := remoteClient.DownloadAgent(context.Background(), remoteEntry, remoteTarget)
		if localErr != nil || remoteErr != nil {
			t.Fatalf("Agent parity for %+v: local=%v remote=%v", ref, localErr, remoteErr)
		}
		localTree := normalizedTree(t, localTarget)
		remoteTree := normalizedTree(t, remoteTarget)
		if !reflect.DeepEqual(localTree, remoteTree) {
			t.Fatalf("Agent tree parity for %+v:\nlocal:  %#v\nremote: %#v", ref, localTree, remoteTree)
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
	writeContractSourceFile(t, filepath.Join(skillRoot, "SKILL.md"), "---\nschema_version: 2\nname: contract-skill\nname_zh: 契约技能\nname_en: Contract Skill\ndescription: Cross-layer Skill\ndescription_zh: 跨层契约技能\nversion: \"v1.2.3\"\n---\n# Contract Skill\n")
	writeContractSourceFile(t, filepath.Join(skillRoot, "skill-card.md"), contractSkillCard)
	pluginRoot := filepath.Join(sourceRoot, "contract-plugin")
	writeContractSourceFile(t, filepath.Join(pluginRoot, "apc-plugin.json"), `{"schema_version":2,"name":"contract-plugin","name_zh":"契约插件","name_en":"Contract Plugin","version":"v2.0.0","description":"Cross-layer Plugin","description_zh":"跨层契约插件","skills":["skills/helper"]}`)
	writeContractSourceFile(t, filepath.Join(pluginRoot, "skills", "helper", "SKILL.md"), "---\nschema_version: 2\nname: helper\nname_zh: 助手技能\nname_en: Helper Skill\ndescription: Embedded helper\ndescription_zh: 内嵌助手\nversion: \"v1.0.0\"\n---\n# Helper\n")
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
		powerType     domain.PowerType
		path          string
		name          string
		version       string
		description   string
		contentType   string
		nameZH        string
		nameEN        string
		descriptionZH string
	}{
		{
			powerType: domain.PowerTypeSkill,
			path:      skillRoot,
			name:      "contract-skill", version: "v1.2.3",
			description: "Cross-layer Skill", contentType: "application/gzip",
			nameZH: "契约技能", nameEN: "Contract Skill", descriptionZH: "跨层契约技能",
		},
		{
			powerType: domain.PowerTypePlugin,
			path:      pluginRoot,
			name:      "contract-plugin", version: "v2.0.0",
			description: "Cross-layer Plugin", contentType: "application/gzip",
			nameZH: "契约插件", nameEN: "Contract Plugin", descriptionZH: "跨层契约插件",
		},
		{
			powerType: domain.PowerTypeMCP,
			path:      filepath.Join(root, "mcps", "filesystem", "v0.1.0", "MCP.json"),
			name:      "filesystem", version: "v0.1.0",
			description: "Filesystem MCP server with a workspace root", contentType: "application/json",
			nameZH: "文件系统", nameEN: "Filesystem MCP", descriptionZH: "带工作区根目录的文件系统 MCP 服务。",
		},
		{
			powerType: domain.PowerTypeAgent,
			path:      filepath.Join(root, "agents", "bianbu-agent-default", "v1.0.0"),
			name:      "bianbu-agent-default", version: "v1.0.0",
			description: "Project-local Bianbu agent", contentType: "application/gzip",
			nameZH: "Bianbu 默认助手", nameEN: "Bianbu Default Agent", descriptionZH: "项目本地的 Bianbu 智能体。",
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
			server.assertReceivedSubmission(t, test.powerType, test.name, test.version, test.description, message, test.contentType,
				test.nameZH, test.nameEN, test.descriptionZH)
			if test.powerType == domain.PowerTypeMCP {
				server.assertReceivedMCPDefinition(t, domain.MCPServer{
					Transport: domain.MCPTransportStdio,
					Command:   "npx",
					Args:      []string{"-y", "@modelcontextprotocol/server-filesystem", "/workspace"},
				})
			}
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
	}, []string{filepath.Join(root, "agents", "bianbu-agent-default", "v1.0.0")})
	if !errors.Is(err, cloud.ErrTooManyActiveSubmissions) || !errors.Is(err, cloud.ErrSubmissionConflict) ||
		!strings.Contains(err.Error(), "TOO_MANY_ACTIVE_SUBMISSIONS") {
		t.Fatalf("Submit() quota error = %v, want typed TOO_MANY_ACTIVE_SUBMISSIONS conflict", err)
	}

	mcpRef := domain.PowerRef{
		Type: domain.PowerTypeMCP, Name: "filesystem", Version: "v0.1.0",
	}
	mcpEntry, err := catalogSnapshotForClient(t, client).Metadata(mcpRef)
	if err != nil {
		t.Fatalf("Catalog Metadata(%+v) error = %v", mcpRef, err)
	}
	mcp, err := client.GetMCP(context.Background(), mcpEntry)
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

const contractSkillCard = `---
schema_version: 2
---
# Skill Card: Contract Fixture

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

func TestCatalogDecodeNormalizesMCPGzipToJSON(t *testing.T) {
	raw := []byte(`{"schema_version":1,"skills":{"schema_version":1,"items":[]},"plugins":{"schema_version":1,"items":[]},"mcps":{"schema_version":1,"items":[{"name":"demo-mcp","version":"v1.0.0","description":"mcp description","content_type":"application/gzip","content_size":1,"content_md5":"0123456789abcdef0123456789abcdef","published_at":"2026-08-25T12:34:56Z","tags":[]}]},"agents":{"schema_version":1,"items":[]}}`)
	snapshot, err := catalog.Decode(raw)
	if err != nil {
		t.Fatalf("catalog.Decode() error = %v, want success with normalized MCP content type", err)
	}
	entries := snapshot.List(domain.PowerTypeMCP)
	if len(entries) != 1 || entries[0].ContentType != "application/json" {
		t.Fatalf("decoded MCP content type = %v, want normalized application/json", entries)
	}
	// domain 层防御性不变量仍拒绝直接声明 gzip 的 MCP 条目。
	entry := domain.CatalogEntry{
		Ref:         domain.PowerRef{Type: domain.PowerTypeMCP, Name: "demo-mcp", Version: "v1.0.0"},
		Description: "mcp description",
		ContentType: "application/gzip",
		ContentSize: 1,
		ContentMD5:  "0123456789abcdef0123456789abcdef",
		PublishedAt: "2026-08-25T12:34:56Z",
		Tags:        []string{},
	}
	if err := entry.Validate(); err == nil {
		t.Fatalf("domain.CatalogEntry.Validate() = nil, want content type mismatch error")
	}
}
