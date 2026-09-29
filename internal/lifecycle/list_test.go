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
	groups     map[domain.AgentID]domain.InstalledPowerGroups
	configErrs map[domain.AgentID]error
	cloudRefs  []domain.PowerRef
	catalog    catalog.Snapshot
	syncStatus catalog.SyncStatus

	supportedAgents []domain.AgentID
	beforeRefresh   func()
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
func (r *listFakeRegistry) SupportedAgents() []domain.AgentID {
	return r.fixture.supportedAgents
}
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

func (c *listFakeConfig) List(agent domain.AgentID) (domain.InstalledPowerGroups, error) {
	*c.calls = append(*c.calls, "config:"+string(agent))
	return c.fixture.groups[agent], c.fixture.configErrs[agent]
}
func (*listFakeConfig) Find(domain.AgentID, domain.PowerType, string) (domain.InstalledPower, bool, error) {
	return domain.InstalledPower{}, false, nil
}
func (*listFakeConfig) Set(domain.AgentID, domain.InstalledPower) error       { return nil }
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
	if want := (cli.ListResult{Powers: []cli.ListedPower{{Power: ref, Description: "test power", Tags: []string{}}}}); !reflect.DeepEqual(result, want) {
		t.Fatalf("List() = %#v, want %#v", result, want)
	}
}

func TestListCatalogFiltersByName(t *testing.T) {
	var calls []string
	beta := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.0.0"}
	other := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "other", Version: "v2.0.0"}
	service := newListService(t, &calls, listFixture{cloudRefs: []domain.PowerRef{beta, other}})

	result, err := service.List(context.Background(), cli.Flags{Name: "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if want := (cli.ListResult{Powers: []cli.ListedPower{{Power: beta, Description: "test power", Tags: []string{}}}}); !reflect.DeepEqual(result, want) {
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
				if err != nil || !reflect.DeepEqual(result, cli.ListResult{Powers: []cli.ListedPower{{Power: ref, Description: "test power", Tags: []string{}}}}) {
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
	contentType := "application/gzip"
	if ref.Type == domain.PowerTypeMCP {
		contentType = "application/json"
	}
	return domain.CatalogEntry{
		Ref: ref, Description: "test power", Message: "test message", ContentType: contentType,
		ContentSize: 1, ContentMD5: "00000000000000000000000000000000", PublishedAt: "2026-08-25T00:00:00Z", Tags: []string{},
	}
}

// TestListAndShowCarryDisplayMetadata 锁定 list/show 结果装配携带 v2 展示字段：
// market（目录条目）与 installed（本地清单）两条路径都要逐字段复制，前端才能按语言显示。
func TestListAndShowCarryDisplayMetadata(t *testing.T) {
	entry := domain.CatalogEntry{
		Ref:           domain.PowerRef{Type: domain.PowerTypeSkill, Name: "brief-status", Version: "v0.0.1"},
		Description:   "Use when writing a concise progress status update.",
		Message:       "fixture",
		ContentType:   "application/gzip",
		ContentSize:   1,
		ContentMD5:    "0123456789abcdef0123456789abcdef",
		PublishedAt:   "2026-08-25T12:34:56Z",
		Tags:          []string{},
		NameZH:        "简报状态",
		NameEN:        "Brief Status",
		DescriptionZH: "用于撰写简洁的进度状态更新。",
	}
	market := listedPowerFromCatalogEntry(entry)
	if market.NameZH != entry.NameZH || market.NameEN != entry.NameEN || market.DescriptionZH != entry.DescriptionZH {
		t.Fatalf("market listed power display fields = %q/%q/%q, want the catalog values",
			market.NameZH, market.NameEN, market.DescriptionZH)
	}
	if show := makeShowResult(entry); show.Power.NameZH != entry.NameZH || show.Power.DescriptionZH != entry.DescriptionZH {
		t.Fatalf("show result display fields = %q/%q, want the catalog values", show.Power.NameZH, show.Power.DescriptionZH)
	}

	installed := listedPowerFromInstalled(domain.AgentBibit, domain.InstalledPower{
		Ref:           entry.Ref,
		Description:   entry.Description,
		Tags:          []string{},
		NameZH:        entry.NameZH,
		NameEN:        entry.NameEN,
		DescriptionZH: entry.DescriptionZH,
	})
	if installed.Agent != domain.AgentBibit || installed.NameZH != entry.NameZH || installed.DescriptionZH != entry.DescriptionZH {
		t.Fatalf("installed listed power = %#v, want the manifest display fields with the agent", installed)
	}
}

// TestSearchResultCarriesNoDisplayMetadata 锁定 search 不是展示字段通道（ADR-040）：
// 即使目录条目带展示字段，search 的 CLI 记录也逐字节保持改动前的字段集。
func TestSearchResultCarriesNoDisplayMetadata(t *testing.T) {
	entry := domain.CatalogEntry{
		Ref:         domain.PowerRef{Type: domain.PowerTypeSkill, Name: "brief-status", Version: "v0.0.1"},
		Description: "Use when writing a concise progress status update.",
		NameZH:      "简报状态", NameEN: "Brief Status", DescriptionZH: "用于撰写简洁的进度状态更新。",
	}
	powers := catalogPowers([]domain.CatalogEntry{entry})
	if len(powers) != 1 {
		t.Fatalf("catalogPowers() = %#v, want one record", powers)
	}
	if powers[0].NameZH != "" || powers[0].NameEN != "" || powers[0].DescriptionZH != "" {
		t.Fatalf("search record display fields = %q/%q/%q, want them deliberately dropped",
			powers[0].NameZH, powers[0].NameEN, powers[0].DescriptionZH)
	}
}
