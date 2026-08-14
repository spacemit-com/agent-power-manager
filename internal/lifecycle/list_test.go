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
	"apm/internal/lock"
	"apm/internal/logging"
	"apm/internal/transaction"
)

type listFixture struct {
	acquireErr error
	recoverErr error
	releaseErr error
	refreshErr error
	cloudErr   error

	recovery   transaction.RecoveryResult
	snapshot   domain.AgentSnapshot
	groups     map[domain.AgentID]domain.PowerGroups
	configErrs map[domain.AgentID]error
	cloudRefs  []domain.PowerRef

	beforeRefresh func()
	beforeRelease func()
}

type listFakeUnlock struct {
	calls   *[]string
	fixture *listFixture
}

func (u *listFakeUnlock) Release() error {
	if u.fixture.beforeRelease != nil {
		u.fixture.beforeRelease()
	}
	*u.calls = append(*u.calls, "release")
	return u.fixture.releaseErr
}

type listFakeLocker struct {
	calls   *[]string
	fixture *listFixture
}

func (l *listFakeLocker) Acquire() (lock.Unlock, error) {
	*l.calls = append(*l.calls, "acquire")
	return &listFakeUnlock{calls: l.calls, fixture: l.fixture}, l.fixture.acquireErr
}

type listFakeTransactions struct {
	calls   *[]string
	fixture *listFixture
}

func (m *listFakeTransactions) Recover(context.Context) (transaction.RecoveryResult, error) {
	*m.calls = append(*m.calls, "recover")
	return m.fixture.recovery, m.fixture.recoverErr
}

func (*listFakeTransactions) Install(context.Context, transaction.InstallRequest) error { return nil }
func (*listFakeTransactions) InstallMCP(context.Context, transaction.MCPInstallRequest) error {
	return nil
}

func (*listFakeTransactions) Uninstall(context.Context, transaction.UninstallRequest) (transaction.UninstallResult, error) {
	return transaction.UninstallResult{}, nil
}
func (*listFakeTransactions) UninstallMCP(context.Context, transaction.MCPUninstallRequest) (transaction.UninstallResult, error) {
	return transaction.UninstallResult{}, nil
}
func (*listFakeTransactions) InstallPlugin(context.Context, transaction.PluginInstallRequest) error {
	return errors.New("unexpected InstallPlugin call")
}
func (*listFakeTransactions) UninstallPlugin(context.Context, transaction.PluginUninstallRequest) (transaction.UninstallResult, error) {
	return transaction.UninstallResult{}, errors.New("unexpected UninstallPlugin call")
}

type listFakeCloud struct {
	calls   *[]string
	fixture *listFixture
}

func (c *listFakeCloud) List() ([]domain.PowerRef, error) {
	*c.calls = append(*c.calls, "cloud-list")
	return c.fixture.cloudRefs, c.fixture.cloudErr
}

func (*listFakeCloud) Get(domain.PowerRef) error { return nil }
func (*listFakeCloud) GetMCP(domain.PowerRef) (domain.MCPServer, error) {
	return domain.MCPServer{}, nil
}

func (*listFakeCloud) GetPlugin(domain.PowerRef) (domain.PluginDefinition, error) {
	return domain.PluginDefinition{}, nil
}

type listFakeRegistry struct {
	calls   *[]string
	fixture *listFixture
}

func (*listFakeRegistry) ResolveSkill(domain.AgentID) (adapter.SkillAdapter, error) {
	return nil, nil
}
func (*listFakeRegistry) ResolveMCP(domain.AgentID) (adapter.MCPAdapter, error) { return nil, nil }
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

func (*listFakeConfig) Set(domain.AgentID, domain.PowerRef) error { return nil }

func (*listFakeConfig) Remove(domain.AgentID, domain.PowerType, string) error { return nil }

func TestNewListRejectsNilDependencies(t *testing.T) {
	var calls []string
	fixture := &listFixture{}
	locker := &listFakeLocker{calls: &calls, fixture: fixture}
	transactions := &listFakeTransactions{calls: &calls, fixture: fixture}
	cloud := &listFakeCloud{calls: &calls, fixture: fixture}
	registry := &listFakeRegistry{calls: &calls, fixture: fixture}
	config := &listFakeConfig{calls: &calls, fixture: fixture}

	tests := []struct {
		name string
		new  func() (*ListService, error)
		want string
	}{
		{name: "locker", new: func() (*ListService, error) { return NewList(nil, transactions, cloud, registry, config) }, want: "create List lifecycle with nil locker"},
		{name: "typed nil locker", new: func() (*ListService, error) {
			return NewList((*listFakeLocker)(nil), transactions, cloud, registry, config)
		}, want: "create List lifecycle with nil locker"},
		{name: "transaction manager", new: func() (*ListService, error) { return NewList(locker, nil, cloud, registry, config) }, want: "create List lifecycle with nil transaction manager"},
		{name: "typed nil transaction manager", new: func() (*ListService, error) {
			return NewList(locker, (*listFakeTransactions)(nil), cloud, registry, config)
		}, want: "create List lifecycle with nil transaction manager"},
		{name: "Cloud client", new: func() (*ListService, error) { return NewList(locker, transactions, nil, registry, config) }, want: "create List lifecycle with nil Cloud client"},
		{name: "typed nil Cloud client", new: func() (*ListService, error) {
			return NewList(locker, transactions, (*listFakeCloud)(nil), registry, config)
		}, want: "create List lifecycle with nil Cloud client"},
		{name: "adapter registry", new: func() (*ListService, error) { return NewList(locker, transactions, cloud, nil, config) }, want: "create List lifecycle with nil adapter registry"},
		{name: "typed nil adapter registry", new: func() (*ListService, error) {
			return NewList(locker, transactions, cloud, (*listFakeRegistry)(nil), config)
		}, want: "create List lifecycle with nil adapter registry"},
		{name: "config repository", new: func() (*ListService, error) { return NewList(locker, transactions, cloud, registry, nil) }, want: "create List lifecycle with nil config repository"},
		{name: "typed nil config repository", new: func() (*ListService, error) {
			return NewList(locker, transactions, cloud, registry, (*listFakeConfig)(nil))
		}, want: "create List lifecycle with nil config repository"},
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

func TestListCloudDoesNotTouchSharedState(t *testing.T) {
	var calls []string
	var stdout, stderr bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(logging.NewLogger(&stdout, &stderr, false))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })

	ref := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}
	service := newListService(t, &calls, listFixture{cloudRefs: []domain.PowerRef{ref}})

	result, err := service.List(context.Background(), cli.Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"cloud-list"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	want := cli.ListResult{Powers: []cli.ListedPower{{Power: ref}}}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("List() = %#v, want %#v", result, want)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("output = stdout %q stderr %q, want no output", stdout.String(), stderr.String())
	}
}

func TestListCloudFailurePreservesCause(t *testing.T) {
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
	if want := []string{"cloud-list"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestListCloudVerboseDoesNotRecoverOrLog(t *testing.T) {
	var calls []string
	var stdout, stderr bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(logging.NewLogger(&stdout, &stderr, true))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })

	service := newListService(t, &calls, listFixture{recovery: transaction.RecoveryResult{Recovered: true}})
	if _, err := service.List(context.Background(), cli.Flags{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"cloud-list"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("output = stdout %q stderr %q, want no output", stdout.String(), stderr.String())
	}
}

func newListService(t *testing.T, calls *[]string, fixture listFixture) *ListService {
	t.Helper()
	service, err := NewList(
		&listFakeLocker{calls: calls, fixture: &fixture},
		&listFakeTransactions{calls: calls, fixture: &fixture},
		&listFakeCloud{calls: calls, fixture: &fixture},
		&listFakeRegistry{calls: calls, fixture: &fixture},
		&listFakeConfig{calls: calls, fixture: &fixture},
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
