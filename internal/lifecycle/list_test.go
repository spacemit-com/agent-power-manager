package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/logging"
	"apm/internal/transaction"
)

type listFixture struct {
	inspectErr error
	refreshErr error
	cloudErr   error

	inspection transaction.InspectionResult
	snapshot   domain.AgentSnapshot
	groups     map[domain.AgentID]domain.PowerGroups
	configErrs map[domain.AgentID]error
	cloudRefs  []domain.PowerRef

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

type listFakeCloud struct {
	calls   *[]string
	fixture *listFixture
}

func (c *listFakeCloud) List() ([]domain.PowerRef, error) {
	*c.calls = append(*c.calls, "cloud-list")
	return c.fixture.cloudRefs, c.fixture.cloudErr
}
func (*listFakeCloud) DownloadSkill(domain.PowerRef) error { return nil }
func (*listFakeCloud) DownloadAgent(domain.PowerRef) error { return nil }
func (*listFakeCloud) GetMCP(domain.PowerRef) (domain.MCPServer, error) {
	return domain.MCPServer{}, nil
}
func (*listFakeCloud) DownloadPlugin(context.Context, domain.PowerRef, string) error {
	return errors.New("unexpected Plugin download")
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
	cloud := &listFakeCloud{calls: &calls, fixture: fixture}
	registry := &listFakeRegistry{calls: &calls, fixture: fixture}
	config := &listFakeConfig{calls: &calls, fixture: fixture}

	tests := []struct {
		name string
		new  func() (*ListService, error)
		want string
	}{
		{"inspector", func() (*ListService, error) { return NewList(nil, cloud, registry, config) }, "create List lifecycle with nil transaction inspector"},
		{"typed nil inspector", func() (*ListService, error) { return NewList((*listFakeInspector)(nil), cloud, registry, config) }, "create List lifecycle with nil transaction inspector"},
		{"Cloud client", func() (*ListService, error) { return NewList(inspector, nil, registry, config) }, "create List lifecycle with nil Cloud client"},
		{"typed nil Cloud client", func() (*ListService, error) { return NewList(inspector, (*listFakeCloud)(nil), registry, config) }, "create List lifecycle with nil Cloud client"},
		{"adapter registry", func() (*ListService, error) { return NewList(inspector, cloud, nil, config) }, "create List lifecycle with nil adapter registry"},
		{"typed nil adapter registry", func() (*ListService, error) { return NewList(inspector, cloud, (*listFakeRegistry)(nil), config) }, "create List lifecycle with nil adapter registry"},
		{"config repository", func() (*ListService, error) { return NewList(inspector, cloud, registry, nil) }, "create List lifecycle with nil config repository"},
		{"typed nil config repository", func() (*ListService, error) { return NewList(inspector, cloud, registry, (*listFakeConfig)(nil)) }, "create List lifecycle with nil config repository"},
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

func TestListCloudInspectsBeforeListing(t *testing.T) {
	var calls []string
	ref := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}
	service := newListService(t, &calls, listFixture{cloudRefs: []domain.PowerRef{ref}})

	result, err := service.List(context.Background(), cli.Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"inspect", "cloud-list"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if want := (cli.ListResult{Powers: []cli.ListedPower{{Power: ref}}}); !reflect.DeepEqual(result, want) {
		t.Fatalf("List() = %#v, want %#v", result, want)
	}
}

func TestListCloudFailurePreservesCauseAfterInspection(t *testing.T) {
	var calls []string
	wantErr := errors.New("Cloud unavailable")
	service := newListService(t, &calls, listFixture{cloudErr: wantErr})

	result, err := service.List(context.Background(), cli.Flags{})
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "list Cloud powers") {
		t.Fatalf("List() error = %v, want wrapped %v", err, wantErr)
	}
	if len(result.Powers) != 0 {
		t.Fatalf("List() result = %#v, want no powers", result)
	}
	if want := []string{"inspect", "cloud-list"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestListCloudJournalWarningsDoNotBlockListing(t *testing.T) {
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
		{"damaged", listFixture{inspectErr: damagedErr, cloudErr: cloudErr}, cloudErr, "transaction journal is damaged"},
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
			} else if !errors.Is(err, tt.wantCloudErr) || !strings.Contains(err.Error(), "list Cloud powers") {
				t.Fatalf("List() error = %v, want Cloud error", err)
			}
			if want := []string{"inspect", "cloud-list"}; !reflect.DeepEqual(calls, want) {
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
	service, err := NewList(
		&listFakeInspector{calls: calls, fixture: &fixture},
		&listFakeCloud{calls: calls, fixture: &fixture},
		&listFakeRegistry{calls: calls, fixture: &fixture},
		&listFakeConfig{calls: calls, fixture: &fixture},
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
