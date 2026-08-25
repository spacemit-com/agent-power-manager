package local_test

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"apm/internal/catalog"
	"apm/internal/cloud"
	"apm/internal/cloud/local"
	"apm/internal/domain"
	"apm/internal/submission"
)

const localCatalogSkillCard = `# Skill Card: Catalog fixture

## Capability
tests catalogs

## Use Cases
catalog tests

## Inputs and Outputs
fixture in, catalog out

## Dependencies
none

## Limitations
tests only

## Maintainer
Owner: catalog-team
Contact: catalog@example.com
`

type localCatalogFixture struct {
	ref         domain.PowerRef
	description string
	path        string
	raw         []byte
}

func catalogEntryFromLocalProvider(t *testing.T, client *local.Client, ref domain.PowerRef) domain.CatalogEntry {
	t.Helper()
	destination := filepath.Join(t.TempDir(), "catalog.json")
	if err := client.DownloadCatalog(context.Background(), destination); err != nil {
		t.Fatalf("DownloadCatalog() for entry %+v: %v", ref, err)
	}
	raw, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := snapshot.Resolve(ref)
	if err != nil {
		t.Fatalf("Resolve(%+v): %v", ref, err)
	}
	return entry
}

func catalogEntryForLocalTest(ref domain.PowerRef) domain.CatalogEntry {
	contentType := "application/octet-stream"
	switch ref.Type {
	case domain.PowerTypeSkill, domain.PowerTypePlugin:
		contentType = "application/gzip"
	case domain.PowerTypeMCP:
		contentType = "application/json"
	case domain.PowerTypeAgent:
		contentType = "application/yaml"
	}
	return domain.CatalogEntry{
		Ref: ref, Description: "test fixture", Message: "test fixture", ContentType: contentType,
		ContentSize: 1, ContentMD5: "00000000000000000000000000000000",
		PublishedAt: "1970-01-01T00:00:00Z", Tags: []string{},
	}
}

func TestLocalCatalogIsDeterministicAndUsesAuthoritativeContent(t *testing.T) {
	firstRoot, firstFixtures := writeLocalCatalogFixtureTree(t, false)
	secondRoot, _ := writeLocalCatalogFixtureTree(t, true)

	firstClient, err := local.New(firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	secondClient, err := local.New(secondRoot)
	if err != nil {
		t.Fatal(err)
	}

	firstVersion, err := firstClient.CatalogVersion(context.Background())
	if err != nil {
		t.Fatalf("first CatalogVersion() error = %v", err)
	}
	repeatedVersion, err := firstClient.CatalogVersion(context.Background())
	if err != nil {
		t.Fatalf("repeated CatalogVersion() error = %v", err)
	}
	secondVersion, err := secondClient.CatalogVersion(context.Background())
	if err != nil {
		t.Fatalf("second-root CatalogVersion() error = %v", err)
	}
	if firstVersion != repeatedVersion || firstVersion != secondVersion {
		t.Fatalf("CatalogVersion() values differ: first=%+v repeated=%+v reordered=%+v", firstVersion, repeatedVersion, secondVersion)
	}

	firstBytes := downloadLocalCatalog(t, firstClient)
	repeatedBytes := downloadLocalCatalog(t, firstClient)
	secondBytes := downloadLocalCatalog(t, secondClient)
	if !reflect.DeepEqual(firstBytes, repeatedBytes) || !reflect.DeepEqual(firstBytes, secondBytes) {
		t.Fatal("DownloadCatalog() bytes differ across calls or fixture creation order")
	}
	wantSHA256 := sha256.Sum256(firstBytes)
	if firstVersion.SHA256 != hex.EncodeToString(wantSHA256[:]) || firstVersion.Size != int64(len(firstBytes)) {
		t.Fatalf("CatalogVersion() = %+v, want SHA-256 %x and size %d", firstVersion, wantSHA256, len(firstBytes))
	}

	assertLocalCatalogGroups(t, firstBytes, 2)
	snapshot, err := catalog.Decode(firstBytes)
	if err != nil {
		t.Fatalf("catalog.Decode() error = %v", err)
	}
	if got := len(snapshot.List("")); got != len(firstFixtures) {
		t.Fatalf("catalog entries = %d, want %d", got, len(firstFixtures))
	}
	for _, fixture := range firstFixtures {
		entry, err := snapshot.Metadata(fixture.ref)
		if err != nil {
			t.Fatalf("Metadata(%+v) error = %v", fixture.ref, err)
		}
		prepared, err := submission.Prepare(context.Background(), submission.Request{
			Type: fixture.ref.Type, Path: fixture.path, Message: "local fixture",
		})
		if err != nil {
			t.Fatalf("prepare authoritative fixture %+v: %v", fixture.ref, err)
		}
		if entry.Description != fixture.description || entry.Description != prepared.Description {
			t.Errorf("Metadata(%+v).Description = %q, want authoritative %q", fixture.ref, entry.Description, fixture.description)
		}
		if entry.Message != "local fixture" || entry.PublishedAt != "1970-01-01T00:00:00Z" || entry.Tags == nil || len(entry.Tags) != 0 {
			t.Errorf("Metadata(%+v) fixed fields = message %q published %q tags %#v", fixture.ref, entry.Message, entry.PublishedAt, entry.Tags)
		}
		if entry.ContentType != prepared.ContentType || entry.ContentSize != int64(len(prepared.Bytes)) || entry.ContentMD5 != prepared.MD5 {
			t.Errorf("Metadata(%+v) content = %q/%d/%q, want %q/%d/%q", fixture.ref, entry.ContentType, entry.ContentSize, entry.ContentMD5, prepared.ContentType, len(prepared.Bytes), prepared.MD5)
		}
	}

	assertLocalCatalogMCPUsesCompleteJCS(t, snapshot)
	assertLocalCatalogAgentUsesRawYAML(t, snapshot, firstFixtures)
}

func TestLocalContentRejectsCatalogEntryMetadataDrift(t *testing.T) {
	root, fixtures := writeLocalCatalogFixtureTree(t, false)
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(string(fixture.ref.Type), func(t *testing.T) {
			entry := catalogEntryFromLocalProvider(t, client, fixture.ref)
			entry.Description += " drift"
			var err error
			switch entry.Ref.Type {
			case domain.PowerTypeSkill:
				err = client.DownloadSkill(context.Background(), entry, filepath.Join(t.TempDir(), "skill"))
			case domain.PowerTypePlugin:
				err = client.DownloadPlugin(context.Background(), entry, filepath.Join(t.TempDir(), "plugin"))
			case domain.PowerTypeMCP:
				_, err = client.GetMCP(context.Background(), entry)
			case domain.PowerTypeAgent:
				err = client.DownloadAgent(context.Background(), entry, filepath.Join(t.TempDir(), "agent.yaml"))
			}
			if !errors.Is(err, cloud.ErrIntegrity) {
				t.Fatalf("content operation with drifted entry error = %v, want ErrIntegrity", err)
			}
		})
	}
}

func TestLocalCatalogDownloadRequiresCallerOwnedDestination(t *testing.T) {
	root, _ := writeLocalCatalogFixtureTree(t, false)
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}

	parent := t.TempDir()
	preexisting := filepath.Join(parent, "existing.json")
	if err := os.WriteFile(preexisting, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		destination string
	}{
		{name: "relative", destination: "catalog.json"},
		{name: "unclean", destination: parent + string(filepath.Separator) + "nested" + string(filepath.Separator) + ".." + string(filepath.Separator) + "catalog.json"},
		{name: "missing parent", destination: filepath.Join(parent, "missing", "catalog.json")},
		{name: "preexisting", destination: preexisting},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := client.DownloadCatalog(context.Background(), test.destination)
			if !errors.Is(err, cloud.ErrCatalogDestination) {
				t.Fatalf("DownloadCatalog(%q) error = %v, want ErrCatalogDestination", test.destination, err)
			}
		})
	}
	if got, err := os.ReadFile(preexisting); err != nil || string(got) != "sentinel" {
		t.Fatalf("preexisting destination = %q, %v, want unchanged sentinel", got, err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	canceledDestination := filepath.Join(parent, "canceled.json")
	if err := client.DownloadCatalog(canceled, canceledDestination); !errors.Is(err, context.Canceled) || errors.Is(err, cloud.ErrCatalogDestination) {
		t.Fatalf("canceled DownloadCatalog() error = %v, want only context.Canceled", err)
	}
	if _, err := os.Lstat(canceledDestination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled destination stat error = %v, want absent", err)
	}
}

func TestLocalCatalogVersionPreservesCancellation(t *testing.T) {
	root, _ := writeLocalCatalogFixtureTree(t, false)
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.CatalogVersion(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CatalogVersion() error = %v, want context.Canceled", err)
	}
}

func TestLocalCatalogRejectsPluginSnapshotOutsideAuthoritativeClosedWorld(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{name: "null skills", mutate: func(t *testing.T, source string) {
			writeLocalCatalogFile(t, filepath.Join(source, "apc-plugin.json"), []byte(`{"schema_version":1,"name":"catalog-plugin","version":"v1.0.0","description":"plugin description v1.0.0","skills":null}`))
		}},
		{name: "duplicate declaration", mutate: func(t *testing.T, source string) {
			writeLocalCatalogFile(t, filepath.Join(source, "apc-plugin.json"), []byte(`{"schema_version":1,"name":"catalog-plugin","version":"v1.0.0","description":"plugin description v1.0.0","skills":["skills/helper","skills/helper"]}`))
		}},
		{name: "noncanonical declaration", mutate: func(t *testing.T, source string) {
			writeLocalCatalogFile(t, filepath.Join(source, "apc-plugin.json"), []byte(`{"schema_version":1,"name":"catalog-plugin","version":"v1.0.0","description":"plugin description v1.0.0","skills":["skills/../helper"]}`))
		}},
		{name: "declared missing", mutate: func(t *testing.T, source string) {
			if err := os.RemoveAll(filepath.Join(source, "skills", "helper")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "undeclared directory", mutate: func(t *testing.T, source string) {
			writeLocalCatalogFile(t, filepath.Join(source, "skills", "extra", "SKILL.md"), []byte("extra"))
			writeLocalCatalogFile(t, filepath.Join(source, "skills", "extra", "skill-card.md"), []byte("extra"))
		}},
		{name: "extra root", mutate: func(t *testing.T, source string) {
			writeLocalCatalogFile(t, filepath.Join(source, "README.md"), []byte("extra"))
		}},
		{name: "native manifest", mutate: func(t *testing.T, source string) {
			writeLocalCatalogFile(t, filepath.Join(source, ".codex-plugin", "plugin.json"), []byte(`{"name":"shadow"}`))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			fixture := writeLocalCatalogPower(t, root, domain.PowerTypePlugin, "v1.0.0")
			test.mutate(t, fixture.path)
			client, err := local.New(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.CatalogVersion(context.Background()); !errors.Is(err, cloud.ErrUnsafeContent) {
				t.Fatalf("CatalogVersion() error = %v, want ErrUnsafeContent", err)
			}
		})
	}
}

func TestLocalCatalogSkillFrontmatterRequiresExactOpener(t *testing.T) {
	for _, test := range []struct {
		name   string
		opener string
	}{
		{name: "leading space", opener: " ---\n"},
		{name: "leading tab", opener: "\t---\n"},
		{name: "inline YAML", opener: "---name: catalog-skill\n"},
		{name: "vertical tab suffix", opener: "--- \v\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			fixture := writeLocalCatalogPower(t, root, domain.PowerTypeSkill, "v1.0.0")
			body := test.opener + "name: catalog-skill\nversion: \"v1.0.0\"\ndescription: skill description v1.0.0\n---\n# Skill\n"
			writeLocalCatalogFile(t, filepath.Join(fixture.path, "SKILL.md"), []byte(body))
			client, err := local.New(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.CatalogVersion(context.Background()); !errors.Is(err, cloud.ErrUnsafeContent) {
				t.Fatalf("CatalogVersion() error = %v, want exact-opener ErrUnsafeContent", err)
			}
		})
	}
}

func TestLocalCatalogSkillFrontmatterAcceptsSpaceTabAndLFOrCRLF(t *testing.T) {
	for _, opener := range []string{"---\n", "--- \t\n", "---\r\n", "--- \t\r\n"} {
		t.Run(hex.EncodeToString([]byte(opener)), func(t *testing.T) {
			root := t.TempDir()
			fixture := writeLocalCatalogPower(t, root, domain.PowerTypeSkill, "v1.0.0")
			body := opener + "name: catalog-skill\nversion: \"v1.0.0\"\ndescription: skill description v1.0.0\n---\n# Skill\n"
			writeLocalCatalogFile(t, filepath.Join(fixture.path, "SKILL.md"), []byte(body))
			client, err := local.New(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.CatalogVersion(context.Background()); err != nil {
				t.Fatalf("CatalogVersion() error = %v", err)
			}
		})
	}
}

func downloadLocalCatalog(t *testing.T, client *local.Client) []byte {
	t.Helper()
	destination := filepath.Join(t.TempDir(), "catalog.json")
	if err := client.DownloadCatalog(context.Background(), destination); err != nil {
		t.Fatalf("DownloadCatalog() error = %v", err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("catalog mode = %04o, want 0600", mode)
	}
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func assertLocalCatalogGroups(t *testing.T, data []byte, wantItems int) {
	t.Helper()
	var envelope struct {
		SchemaVersion int `json:"schema_version"`
		Skills        struct {
			SchemaVersion int               `json:"schema_version"`
			Items         []json.RawMessage `json:"items"`
		} `json:"skills"`
		Plugins struct {
			SchemaVersion int               `json:"schema_version"`
			Items         []json.RawMessage `json:"items"`
		} `json:"plugins"`
		MCPs struct {
			SchemaVersion int               `json:"schema_version"`
			Items         []json.RawMessage `json:"items"`
		} `json:"mcps"`
		Agents struct {
			SchemaVersion int               `json:"schema_version"`
			Items         []json.RawMessage `json:"items"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != 1 {
		t.Fatalf("catalog schema = %d, want 1", envelope.SchemaVersion)
	}
	groups := []struct {
		name          string
		schemaVersion int
		items         []json.RawMessage
	}{
		{name: "skills", schemaVersion: envelope.Skills.SchemaVersion, items: envelope.Skills.Items},
		{name: "plugins", schemaVersion: envelope.Plugins.SchemaVersion, items: envelope.Plugins.Items},
		{name: "mcps", schemaVersion: envelope.MCPs.SchemaVersion, items: envelope.MCPs.Items},
		{name: "agents", schemaVersion: envelope.Agents.SchemaVersion, items: envelope.Agents.Items},
	}
	for _, group := range groups {
		if group.schemaVersion != 1 || group.items == nil || len(group.items) != wantItems {
			t.Errorf("catalog group %s = schema %d items %#v, want schema 1 and %d non-null items", group.name, group.schemaVersion, group.items, wantItems)
		}
	}
}

func assertLocalCatalogMCPUsesCompleteJCS(t *testing.T, snapshot catalog.Snapshot) {
	t.Helper()
	ref := domain.PowerRef{Type: domain.PowerTypeMCP, Name: "catalog-mcp", Version: "v2.0.0"}
	entry, err := snapshot.Metadata(ref)
	if err != nil {
		t.Fatal(err)
	}
	const wantJCS = `{"definition":{"args":["--version","v2.0.0"],"command":"catalog-mcp","transport":"stdio"},"description":"mcp description v2.0.0","name":"catalog-mcp","schema_version":1,"version":"v2.0.0"}`
	digest := md5.Sum([]byte(wantJCS))
	if entry.ContentSize != int64(len(wantJCS)) || entry.ContentMD5 != hex.EncodeToString(digest[:]) {
		t.Fatalf("MCP metadata = size %d MD5 %q, want complete JCS size %d MD5 %x", entry.ContentSize, entry.ContentMD5, len(wantJCS), digest)
	}
}

func assertLocalCatalogAgentUsesRawYAML(t *testing.T, snapshot catalog.Snapshot, fixtures []localCatalogFixture) {
	t.Helper()
	ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "catalog-agent", Version: "v2.0.0"}
	entry, err := snapshot.Metadata(ref)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	for _, fixture := range fixtures {
		if fixture.ref == ref {
			raw = fixture.raw
			break
		}
	}
	digest := md5.Sum(raw)
	if entry.ContentSize != int64(len(raw)) || entry.ContentMD5 != hex.EncodeToString(digest[:]) {
		t.Fatalf("Agent metadata = size %d MD5 %q, want raw YAML size %d MD5 %x", entry.ContentSize, entry.ContentMD5, len(raw), digest)
	}
}

func writeLocalCatalogFixtureTree(t *testing.T, reverse bool) (string, []localCatalogFixture) {
	t.Helper()
	root := t.TempDir()
	typeVersion := []domain.PowerType{domain.PowerTypeSkill, domain.PowerTypePlugin, domain.PowerTypeMCP, domain.PowerTypeAgent}
	versions := []string{"v1.0.0", "v2.0.0"}
	if reverse {
		typeVersion = []domain.PowerType{domain.PowerTypeAgent, domain.PowerTypeMCP, domain.PowerTypePlugin, domain.PowerTypeSkill}
		versions = []string{"v2.0.0", "v1.0.0"}
	}
	fixtures := make([]localCatalogFixture, 0, 8)
	for _, powerType := range typeVersion {
		for _, version := range versions {
			fixture := writeLocalCatalogPower(t, root, powerType, version)
			fixtures = append(fixtures, fixture)
		}
	}
	return root, fixtures
}

func writeLocalCatalogPower(t *testing.T, root string, powerType domain.PowerType, version string) localCatalogFixture {
	t.Helper()
	name := "catalog-" + string(powerType)
	ref := domain.PowerRef{Type: powerType, Name: name, Version: version}
	description := string(powerType) + " description " + version
	var sourcePath string
	var raw []byte
	switch powerType {
	case domain.PowerTypeSkill:
		sourcePath = filepath.Join(root, "skills", name, version)
		writeLocalCatalogFile(t, filepath.Join(sourcePath, "SKILL.md"), []byte("---\nname: "+name+"\nversion: \""+version+"\"\ndescription: "+description+"\n---\n# Catalog Skill\n"))
		writeLocalCatalogFile(t, filepath.Join(sourcePath, "skill-card.md"), []byte(localCatalogSkillCard))
	case domain.PowerTypePlugin:
		sourcePath = filepath.Join(root, "plugins", name, version)
		manifest := []byte(`{"schema_version":1,"name":"` + name + `","version":"` + version + `","description":"` + description + `","skills":["skills/helper"]}`)
		writeLocalCatalogFile(t, filepath.Join(sourcePath, "apc-plugin.json"), manifest)
		writeLocalCatalogFile(t, filepath.Join(sourcePath, "skills", "helper", "SKILL.md"), []byte("---\nname: embedded-helper\nversion: \""+version+"\"\ndescription: embedded description must not win\n---\n# Embedded Skill\n"))
		writeLocalCatalogFile(t, filepath.Join(sourcePath, "skills", "helper", "skill-card.md"), []byte(localCatalogSkillCard))
	case domain.PowerTypeMCP:
		sourcePath = filepath.Join(root, "mcps", name, version, "MCP.json")
		raw = []byte("{\n  \"version\": \"" + version + "\",\n  \"name\": \"" + name + "\",\n  \"schema_version\": 1,\n  \"definition\": {\"transport\": \"stdio\", \"args\": [\"--version\", \"" + version + "\"], \"command\": \"catalog-mcp\"},\n  \"description\": \"" + description + "\"\n}\n")
		writeLocalCatalogFile(t, sourcePath, raw)
	case domain.PowerTypeAgent:
		sourcePath = filepath.Join(root, "agents", name, version, "agent.yaml")
		raw = []byte("# preserved raw YAML\nschema_version: 1\nagent:\n  id: " + name + "\n  version: " + version + "\n  description: " + description + "\n  tools: ['*']\n")
		writeLocalCatalogFile(t, sourcePath, raw)
	default:
		t.Fatalf("unsupported fixture type %q", powerType)
	}
	return localCatalogFixture{ref: ref, description: description, path: sourcePath, raw: raw}
}

func writeLocalCatalogFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatal(err)
	}
}
