package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"apm/internal/adapter"
	"apm/internal/catalog"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/logging"
	"apm/internal/transaction"
)

type listFixture struct {
	inspectErr error
	refreshErr error
	managerErr error

	inspection transaction.InspectionResult
	snapshot   domain.AgentSnapshot
	groups     map[domain.AgentID]domain.PowerGroups
	configErrs map[domain.AgentID]error
	cloudRefs  []domain.PowerRef
	catalog    catalog.Snapshot
	syncStatus catalog.SyncStatus

	beforeRefresh func()
}

type listFakeInspector struct {
	calls   *[]string
	fixture *listFixture
}

func (i *listFakeInspector) Inspect(context.Context) (transaction.InspectionResult, error) {
	*i.calls = append(*i.calls, "inspect")
	return i.fixture.inspection, i.fixture.inspectErr
}

type listFakeCatalogManager struct {
	calls   *[]string
	fixture *listFixture
}

func (c *listFakeCatalogManager) Snapshot(ctx context.Context) (catalog.Snapshot, catalog.SyncStatus, error) {
	*c.calls = append(*c.calls, "catalog-snapshot")
	if err := ctx.Err(); err != nil {
		return catalog.Snapshot{}, catalog.SyncStatus{}, err
	}
	return c.fixture.catalog, c.fixture.syncStatus, c.fixture.managerErr
}

func (c *listFakeCatalogManager) SnapshotLocked(context.Context) (catalog.Snapshot, catalog.SyncStatus, error) {
	*c.calls = append(*c.calls, "catalog-snapshot-locked")
	return c.fixture.catalog, c.fixture.syncStatus, c.fixture.managerErr
}

type listFakeRegistry struct {
	calls   *[]string
	fixture *listFixture
}

func (*listFakeRegistry) ResolveSkill(domain.AgentID) (adapter.SkillAdapter, error) { return nil, nil }
func (*listFakeRegistry) ResolveMCP(domain.AgentID) (adapter.MCPAdapter, error)     { return nil, nil }
func (*listFakeRegistry) ResolvePlugin(domain.AgentID) (adapter.PluginAdapter, error) {
	return nil, nil
}
func (*listFakeRegistry) ResolveAgent(domain.AgentID) (adapter.AgentAdapter, error) { return nil, nil }
func (r *listFakeRegistry) RefreshInstalled() (domain.AgentSnapshot, error) {
	if r.fixture.beforeRefresh != nil {
		r.fixture.beforeRefresh()
	}
	*r.calls = append(*r.calls, "refresh")
	return r.fixture.snapshot, r.fixture.refreshErr
}

type listFakeConfig struct {
	calls   *[]string
	fixture *listFixture
}

func (c *listFakeConfig) List(agent domain.AgentID) (domain.PowerGroups, error) {
	*c.calls = append(*c.calls, "config:"+string(agent))
	return c.fixture.groups[agent], c.fixture.configErrs[agent]
}
func (*listFakeConfig) Find(domain.AgentID, domain.PowerType, string) (domain.PowerRef, bool, error) {
	return domain.PowerRef{}, false, nil
}
func (*listFakeConfig) Set(domain.AgentID, domain.PowerRef) error             { return nil }
func (*listFakeConfig) Remove(domain.AgentID, domain.PowerType, string) error { return nil }

func TestNewListRejectsNilDependencies(t *testing.T) {
	var calls []string
	fixture := &listFixture{}
	inspector := &listFakeInspector{calls: &calls, fixture: fixture}
	manager := &listFakeCatalogManager{calls: &calls, fixture: fixture}
	registry := &listFakeRegistry{calls: &calls, fixture: fixture}
	config := &listFakeConfig{calls: &calls, fixture: fixture}

	tests := []struct {
		name string
		new  func() (*ListService, error)
		want string
	}{
		{"inspector", func() (*ListService, error) { return NewList(nil, manager, registry, config) }, "create List lifecycle with nil transaction inspector"},
		{"typed nil inspector", func() (*ListService, error) { return NewList((*listFakeInspector)(nil), manager, registry, config) }, "create List lifecycle with nil transaction inspector"},
		{"catalog manager", func() (*ListService, error) { return NewList(inspector, nil, registry, config) }, "create List lifecycle with nil catalog manager"},
		{"typed nil catalog manager", func() (*ListService, error) {
			return NewList(inspector, (*listFakeCatalogManager)(nil), registry, config)
		}, "create List lifecycle with nil catalog manager"},
		{"adapter registry", func() (*ListService, error) { return NewList(inspector, manager, nil, config) }, "create List lifecycle with nil adapter registry"},
		{"typed nil adapter registry", func() (*ListService, error) { return NewList(inspector, manager, (*listFakeRegistry)(nil), config) }, "create List lifecycle with nil adapter registry"},
		{"config repository", func() (*ListService, error) { return NewList(inspector, manager, registry, nil) }, "create List lifecycle with nil config repository"},
		{"typed nil config repository", func() (*ListService, error) { return NewList(inspector, manager, registry, (*listFakeConfig)(nil)) }, "create List lifecycle with nil config repository"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := tt.new()
			if service != nil || err == nil || err.Error() != tt.want {
				t.Fatalf("NewList() = (%v, %v), want (nil, %q)", service, err, tt.want)
			}
		})
	}
}

func TestListCatalogInspectsBeforeTakingOneSnapshot(t *testing.T) {
	var calls []string
	ref := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}
	service := newListService(t, &calls, listFixture{cloudRefs: []domain.PowerRef{ref}})

	result, err := service.List(context.Background(), cli.Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"inspect", "catalog-snapshot"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if want := (cli.ListResult{Powers: []cli.ListedPower{{Power: ref}}}); !reflect.DeepEqual(result, want) {
		t.Fatalf("List() = %#v, want %#v", result, want)
	}
}

func TestListCatalogFailurePreservesCauseAfterInspection(t *testing.T) {
	var calls []string
	wantErr := errors.New("Cloud unavailable")
	service := newListService(t, &calls, listFixture{managerErr: wantErr})

	result, err := service.List(context.Background(), cli.Flags{})
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "snapshot catalog powers") {
		t.Fatalf("List() error = %v, want wrapped %v", err, wantErr)
	}
	if len(result.Powers) != 0 {
		t.Fatalf("List() result = %#v, want no powers", result)
	}
	if want := []string{"inspect", "catalog-snapshot"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestListCatalogJournalWarningsDoNotBlockListing(t *testing.T) {
	ref := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}
	damagedErr := errors.New("damaged journal")
	cloudErr := errors.New("Cloud unavailable")
	tests := []struct {
		name         string
		fixture      listFixture
		wantCloudErr error
		wantLog      string
	}{
		{"active", listFixture{inspection: transaction.InspectionResult{Active: true, Operation: transaction.OperationInstall, Agent: domain.AgentCodex, Power: ref}, cloudRefs: []domain.PowerRef{ref}}, nil, "transaction recovery required"},
		{"damaged", listFixture{inspectErr: damagedErr, managerErr: cloudErr}, cloudErr, "transaction journal is damaged"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			var stdout, stderr bytes.Buffer
			originalLogger := slog.Default()
			slog.SetDefault(logging.NewLogger(&stdout, &stderr, false))
			t.Cleanup(func() { slog.SetDefault(originalLogger) })
			service := newListService(t, &calls, tt.fixture)

			result, err := service.List(context.Background(), cli.Flags{})
			if tt.name == "active" {
				if err != nil || !reflect.DeepEqual(result, cli.ListResult{Powers: []cli.ListedPower{{Power: ref}}}) {
					t.Fatalf("List() = (%#v, %v), want Cloud result without error", result, err)
				}
			} else if !errors.Is(err, tt.wantCloudErr) || !strings.Contains(err.Error(), "snapshot catalog powers") {
				t.Fatalf("List() error = %v, want Cloud error", err)
			}
			if want := []string{"inspect", "catalog-snapshot"}; !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls = %v, want %v", calls, want)
			}
			if !strings.Contains(stderr.String(), tt.wantLog) || stdout.Len() != 0 {
				t.Fatalf("output = stdout %q stderr %q, want warning %q", stdout.String(), stderr.String(), tt.wantLog)
			}
		})
	}
}

func newListService(t *testing.T, calls *[]string, fixture listFixture) *ListService {
	t.Helper()
	fixture.catalog = catalogSnapshotFromRefs(t, fixture.cloudRefs)
	service, err := NewList(
		&listFakeInspector{calls: calls, fixture: &fixture},
		&listFakeCatalogManager{calls: calls, fixture: &fixture},
		&listFakeRegistry{calls: calls, fixture: &fixture},
		&listFakeConfig{calls: calls, fixture: &fixture},
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestListCatalogStaleWarningIsRenderedOnceOutsideResult(t *testing.T) {
	var calls []string
	var stdout, stderr bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(logging.NewLogger(&stdout, &stderr, false))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })
	cause := errors.New("catalog source unavailable")
	ref := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}
	service := newListService(t, &calls, listFixture{
		cloudRefs:  []domain.PowerRef{ref},
		syncStatus: catalog.SyncStatus{Stale: true, Cause: cause},
	})

	result, err := service.List(context.Background(), cli.Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"inspect", "catalog-snapshot"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if got := strings.Count(stderr.String(), "level=WARN"); got != 1 || !strings.Contains(stderr.String(), "catalog snapshot is stale") || !strings.Contains(stderr.String(), cause.Error()) || stdout.Len() != 0 {
		t.Fatalf("output = stdout %q stderr %q, want one stale warning", stdout.String(), stderr.String())
	}
	data, marshalErr := json.Marshal(result)
	if marshalErr != nil || strings.Contains(string(data), "stale") || strings.Contains(string(data), cause.Error()) {
		t.Fatalf("JSON result = %q, %v, want no warning data", data, marshalErr)
	}
}

// catalogSnapshotFromRefs 为生命周期测试构造只读的有效目录快照。
func catalogSnapshotFromRefs(t *testing.T, refs []domain.PowerRef) catalog.Snapshot {
	t.Helper()
	entries := make([]domain.CatalogEntry, 0, len(refs))
	for _, ref := range refs {
		entries = append(entries, catalogEntryFromRef(ref))
	}
	data, err := catalog.Encode(entries)
	if err != nil {
		t.Fatalf("encode test catalog: %v", err)
	}
	snapshot, err := catalog.Decode(data)
	if err != nil {
		t.Fatalf("decode test catalog: %v", err)
	}
	return snapshot
}

func catalogEntryFromRef(ref domain.PowerRef) domain.CatalogEntry {
	contentType := "application/json"
	switch ref.Type {
	case domain.PowerTypeSkill, domain.PowerTypePlugin:
		contentType = "application/gzip"
	case domain.PowerTypeAgent:
		contentType = "application/yaml"
	}
	return domain.CatalogEntry{
		Ref: ref, Description: "test power", Message: "test message", ContentType: contentType,
		ContentSize: 1, ContentMD5: "00000000000000000000000000000000", PublishedAt: "2026-08-25T00:00:00Z", Tags: []string{},
	}
}
