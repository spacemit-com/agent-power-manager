package lifecycle

import (
	"bytes"
	"context"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"apm/internal/adapter"
	"apm/internal/cache"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/lock"
	"apm/internal/logging"
	"apm/internal/transaction"
)

type installFixture struct {
	recovery       transaction.RecoveryResult
	snapshot       domain.AgentSnapshot
	cloudRefs      []domain.PowerRef
	selected       string
	installed      map[domain.AgentID]domain.PowerRef
	cacheResponses map[domain.AgentID][]installCacheResponse

	currentAgent domain.AgentID
	cacheCalls   map[domain.AgentID]int

	beforeRefresh func()
	beforeRelease func()
}

type installCacheResponse struct {
	staging string
	err     error
}

type installFakeUnlock struct {
	calls   *[]string
	fixture *installFixture
}

func (u *installFakeUnlock) Release() error {
	if u.fixture.beforeRelease != nil {
		u.fixture.beforeRelease()
	}
	*u.calls = append(*u.calls, "release")
	return nil
}

type installFakeLocker struct {
	calls   *[]string
	fixture *installFixture
}

func (l *installFakeLocker) Acquire() (lock.Unlock, error) {
	*l.calls = append(*l.calls, "acquire")
	return &installFakeUnlock{calls: l.calls, fixture: l.fixture}, nil
}

type installFakeTransactions struct {
	calls   *[]string
	fixture *installFixture
}

func (m *installFakeTransactions) Recover(context.Context) (transaction.RecoveryResult, error) {
	*m.calls = append(*m.calls, "recover")
	return m.fixture.recovery, nil
}

func (m *installFakeTransactions) Install(_ context.Context, request transaction.InstallRequest) error {
	*m.calls = append(*m.calls, "transaction:install:"+string(request.Agent))
	return nil
}

func (*installFakeTransactions) Uninstall(context.Context, transaction.UninstallRequest) error {
	return nil
}

type installFakeCloud struct {
	calls   *[]string
	fixture *installFixture
}

func (c *installFakeCloud) List() ([]domain.PowerRef, error) {
	*c.calls = append(*c.calls, "cloud:list")
	return c.fixture.cloudRefs, nil
}

func (c *installFakeCloud) Get(domain.PowerRef) error {
	*c.calls = append(*c.calls, "cloud:get")
	return nil
}

type installFakeCache struct {
	calls   *[]string
	fixture *installFixture
}

func (c *installFakeCache) Fetch(domain.PowerRef) (string, error) {
	agent := c.fixture.currentAgent
	*c.calls = append(*c.calls, "cache:fetch:"+string(agent))
	if c.fixture.cacheCalls == nil {
		c.fixture.cacheCalls = make(map[domain.AgentID]int)
	}
	call := c.fixture.cacheCalls[agent]
	c.fixture.cacheCalls[agent] = call + 1
	responses := c.fixture.cacheResponses[agent]
	if call < len(responses) {
		return responses[call].staging, responses[call].err
	}
	return "/staging/" + string(agent), nil
}

type installFakeRegistry struct {
	calls   *[]string
	fixture *installFixture
}

func (r *installFakeRegistry) Resolve(agent domain.AgentID, _ domain.PowerType) (adapter.Adapter, error) {
	r.fixture.currentAgent = agent
	*r.calls = append(*r.calls, "resolve:"+string(agent))
	return installFakeAdapter{}, nil
}

func (r *installFakeRegistry) RefreshInstalled() (domain.AgentSnapshot, error) {
	if r.fixture.beforeRefresh != nil {
		r.fixture.beforeRefresh()
	}
	*r.calls = append(*r.calls, "refresh")
	return r.fixture.snapshot, nil
}

type installFakeAdapter struct{}

func (installFakeAdapter) DetectAgent() (bool, error) { return true, nil }

func (installFakeAdapter) TargetPath(domain.PowerRef) (string, error) { return "/target", nil }

func (installFakeAdapter) Install(domain.PowerRef, string) (adapter.InstallResult, error) {
	return adapter.InstallResult{Published: true}, nil
}

type installFakeConfig struct {
	calls   *[]string
	fixture *installFixture
}

func (c *installFakeConfig) Find(agent domain.AgentID, _ domain.PowerType, _ string) (domain.PowerRef, bool, error) {
	*c.calls = append(*c.calls, "config:find:"+string(agent))
	ref, ok := c.fixture.installed[agent]
	return ref, ok, nil
}

func (*installFakeConfig) List(domain.AgentID) (domain.PowerGroups, error) { return nil, nil }

func (*installFakeConfig) Set(domain.AgentID, domain.PowerRef) error { return nil }

func (*installFakeConfig) Remove(domain.AgentID, domain.PowerType, string) error { return nil }

type installFakeVersions struct {
	selected string
}

func (s *installFakeVersions) Select([]string, string) (string, error) { return s.selected, nil }

func TestInstallFirstInstallOrdersDependenciesAndReturnsInstalled(t *testing.T) {
	var calls []string
	service := newInstallService(t, &calls, installFixture{
		snapshot:  domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		cloudRefs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}},
		selected:  "v1.0.0",
	})

	result, err := service.Install(context.Background(), cli.Flags{Agent: "codex", Type: "skill", Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	wantEvents := []string{
		"acquire", "recover", "refresh", "cloud:list",
		"resolve:codex", "config:find:codex", "cache:fetch:codex",
		"transaction:install:codex", "release",
	}
	assertEventSubsequence(t, calls, wantEvents)
	assertInstallItems(t, result, []cli.InstallItem{{
		Agent:   domain.AgentCodex,
		Power:   domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"},
		Outcome: cli.InstallOutcomeInstalled,
	}})
}

func TestInstallCacheMissGetsCloudAndFetchesAgain(t *testing.T) {
	var calls []string
	service := newInstallService(t, &calls, installFixture{
		snapshot:  domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		cloudRefs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}},
		selected:  "v1.0.0",
		cacheResponses: map[domain.AgentID][]installCacheResponse{
			domain.AgentCodex: {{err: cache.ErrMiss}, {staging: "/staging/codex"}},
		},
	})

	result, err := service.Install(context.Background(), cli.Flags{Agent: "codex", Type: "skill", Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{
		"acquire", "recover", "refresh", "cloud:list", "resolve:codex", "config:find:codex",
		"cache:fetch:codex", "cloud:get", "cache:fetch:codex", "transaction:install:codex", "release",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	assertInstallItems(t, result, []cli.InstallItem{{
		Agent:   domain.AgentCodex,
		Power:   domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"},
		Outcome: cli.InstallOutcomeInstalled,
	}})
}

func TestInstallSameVersionReturnsUnchangedWithoutFetchOrTransaction(t *testing.T) {
	var calls []string
	service := newInstallService(t, &calls, installFixture{
		snapshot:  domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		cloudRefs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}},
		selected:  "v1.0.0",
		installed: map[domain.AgentID]domain.PowerRef{
			domain.AgentCodex: {Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"},
		},
	})

	result, err := service.Install(context.Background(), cli.Flags{Agent: "codex", Type: "skill", Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{
		"acquire", "recover", "refresh", "cloud:list", "resolve:codex", "config:find:codex", "release",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	assertInstallItems(t, result, []cli.InstallItem{{
		Agent:   domain.AgentCodex,
		Power:   domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"},
		Outcome: cli.InstallOutcomeUnchanged,
	}})
}

func TestInstallClassifiesUpgradeAndDowngradeBySemVer(t *testing.T) {
	tests := []struct {
		name     string
		previous string
		target   string
		outcome  cli.InstallOutcome
	}{
		{name: "upgrade", previous: "v1.0.0", target: "v2.0.0", outcome: cli.InstallOutcomeUpgraded},
		{name: "downgrade", previous: "v3.0.0", target: "v2.0.0", outcome: cli.InstallOutcomeDowngraded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			service := newInstallService(t, &calls, installFixture{
				snapshot:  domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
				cloudRefs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: tt.target}},
				selected:  tt.target,
				installed: map[domain.AgentID]domain.PowerRef{
					domain.AgentCodex: {Name: "demo", Type: domain.PowerTypeSkill, Version: tt.previous},
				},
			})

			result, err := service.Install(context.Background(), cli.Flags{Agent: "codex", Type: "skill", Name: "demo"})
			if err != nil {
				t.Fatal(err)
			}
			if !containsEvent(calls, "transaction:install:codex") {
				t.Fatalf("calls = %v, want install transaction", calls)
			}
			assertInstallItems(t, result, []cli.InstallItem{{
				Agent:   domain.AgentCodex,
				Power:   domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: tt.target},
				Outcome: tt.outcome,
			}})
		})
	}
}

func TestInstallAllUsesSnapshotOrderAndFetchesSeparatelyPerChangedAgent(t *testing.T) {
	var calls []string
	service := newInstallService(t, &calls, installFixture{
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{
			domain.AgentClaudeCode, domain.AgentCodex, domain.AgentHermes,
		}},
		cloudRefs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}},
		selected:  "v2.0.0",
		installed: map[domain.AgentID]domain.PowerRef{
			domain.AgentCodex: {Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"},
		},
	})

	result, err := service.Install(context.Background(), cli.Flags{Agent: "all", Type: "skill", Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{
		"acquire", "recover", "refresh", "cloud:list",
		"resolve:claude-code", "config:find:claude-code", "cache:fetch:claude-code", "transaction:install:claude-code",
		"resolve:codex", "config:find:codex",
		"resolve:hermes-agent", "config:find:hermes-agent", "cache:fetch:hermes-agent", "transaction:install:hermes-agent",
		"release",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	assertInstallItems(t, result, []cli.InstallItem{
		{Agent: domain.AgentClaudeCode, Power: domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}, Outcome: cli.InstallOutcomeInstalled},
		{Agent: domain.AgentCodex, Power: domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}, Outcome: cli.InstallOutcomeUnchanged},
		{Agent: domain.AgentHermes, Power: domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}, Outcome: cli.InstallOutcomeInstalled},
	})
}

func TestInstallRecoveryWarnIsWrittenBeforeRefreshAndRelease(t *testing.T) {
	var calls []string
	var stdout, stderr bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(logging.NewLogger(&stdout, &stderr, false))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })

	assertWarn := func(point string) {
		if !strings.Contains(stderr.String(), "level=WARN") {
			t.Fatalf("stderr before %s = %q, want recovery WARN", point, stderr.String())
		}
	}
	service := newInstallService(t, &calls, installFixture{
		recovery: transaction.RecoveryResult{
			Recovered:     true,
			Operation:     transaction.OperationInstall,
			Agent:         domain.AgentCodex,
			Power:         domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"},
			OriginalPhase: transaction.PhasePublishing,
			Direction:     transaction.RecoveryCommit,
			Actions:       []transaction.RecoveryAction{{Kind: transaction.RecoveryActionSetManifest}},
		},
		snapshot:      domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		cloudRefs:     []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}},
		selected:      "v2.0.0",
		beforeRefresh: func() { assertWarn("refresh") },
		beforeRelease: func() { assertWarn("release") },
	})

	if _, err := service.Install(context.Background(), cli.Flags{Agent: "codex", Type: "skill", Name: "demo"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"level=WARN", "msg=\"transaction recovered\"", "agent=codex", "power=demo", "version=v2.0.0",
		"phase=publishing", "direction=commit", "actions=",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want %q", stderr.String(), want)
		}
	}
}

func TestInstallNoJournalIsSilentUnlessVerbose(t *testing.T) {
	tests := []struct {
		name        string
		verbose     bool
		wantMessage bool
	}{
		{name: "silent", verbose: false},
		{name: "verbose", verbose: true, wantMessage: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			var stdout, stderr bytes.Buffer
			originalLogger := slog.Default()
			slog.SetDefault(logging.NewLogger(&stdout, &stderr, tt.verbose))
			t.Cleanup(func() { slog.SetDefault(originalLogger) })

			service := newInstallService(t, &calls, installFixture{
				snapshot:  domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
				cloudRefs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}},
				selected:  "v1.0.0",
			})
			if _, err := service.Install(context.Background(), cli.Flags{Agent: "codex", Type: "skill", Name: "demo"}); err != nil {
				t.Fatal(err)
			}
			if tt.wantMessage {
				if !strings.Contains(stderr.String(), "msg=\"no active transaction before install\"") {
					t.Fatalf("stderr = %q, want install recovery debug message", stderr.String())
				}
				return
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("output = stdout %q stderr %q, want no output", stdout.String(), stderr.String())
			}
		})
	}
}

func TestNewInstallRejectsNilAndTypedNilDependencies(t *testing.T) {
	var calls []string
	fixture := &installFixture{}
	locker := &installFakeLocker{calls: &calls, fixture: fixture}
	transactions := &installFakeTransactions{calls: &calls, fixture: fixture}
	cloud := &installFakeCloud{calls: &calls, fixture: fixture}
	cache := &installFakeCache{calls: &calls, fixture: fixture}
	registry := &installFakeRegistry{calls: &calls, fixture: fixture}
	config := &installFakeConfig{calls: &calls, fixture: fixture}
	versions := &installFakeVersions{}

	tests := []struct {
		name string
		new  func() (*InstallService, error)
		want string
	}{
		{name: "locker", new: func() (*InstallService, error) {
			return NewInstall(nil, transactions, cloud, cache, registry, config, versions)
		}, want: "create Install lifecycle with nil locker"},
		{name: "typed nil locker", new: func() (*InstallService, error) {
			return NewInstall((*installFakeLocker)(nil), transactions, cloud, cache, registry, config, versions)
		}, want: "create Install lifecycle with nil locker"},
		{name: "transaction manager", new: func() (*InstallService, error) {
			return NewInstall(locker, nil, cloud, cache, registry, config, versions)
		}, want: "create Install lifecycle with nil transaction manager"},
		{name: "typed nil transaction manager", new: func() (*InstallService, error) {
			return NewInstall(locker, (*installFakeTransactions)(nil), cloud, cache, registry, config, versions)
		}, want: "create Install lifecycle with nil transaction manager"},
		{name: "Cloud client", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, nil, cache, registry, config, versions)
		}, want: "create Install lifecycle with nil Cloud client"},
		{name: "typed nil Cloud client", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, (*installFakeCloud)(nil), cache, registry, config, versions)
		}, want: "create Install lifecycle with nil Cloud client"},
		{name: "power cache", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, cloud, nil, registry, config, versions)
		}, want: "create Install lifecycle with nil power cache"},
		{name: "typed nil power cache", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, cloud, (*installFakeCache)(nil), registry, config, versions)
		}, want: "create Install lifecycle with nil power cache"},
		{name: "adapter registry", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, cloud, cache, nil, config, versions)
		}, want: "create Install lifecycle with nil adapter registry"},
		{name: "typed nil adapter registry", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, cloud, cache, (*installFakeRegistry)(nil), config, versions)
		}, want: "create Install lifecycle with nil adapter registry"},
		{name: "config repository", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, cloud, cache, registry, nil, versions)
		}, want: "create Install lifecycle with nil config repository"},
		{name: "typed nil config repository", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, cloud, cache, registry, (*installFakeConfig)(nil), versions)
		}, want: "create Install lifecycle with nil config repository"},
		{name: "version selector", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, cloud, cache, registry, config, nil)
		}, want: "create Install lifecycle with nil version selector"},
		{name: "typed nil version selector", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, cloud, cache, registry, config, (*installFakeVersions)(nil))
		}, want: "create Install lifecycle with nil version selector"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := tt.new()
			if service != nil || err == nil || err.Error() != tt.want {
				t.Fatalf("NewInstall() = (%v, %v), want (nil, %q)", service, err, tt.want)
			}
		})
	}
}

func newInstallService(t *testing.T, calls *[]string, fixture installFixture) *InstallService {
	t.Helper()
	service, err := NewInstall(
		&installFakeLocker{calls: calls, fixture: &fixture},
		&installFakeTransactions{calls: calls, fixture: &fixture},
		&installFakeCloud{calls: calls, fixture: &fixture},
		&installFakeCache{calls: calls, fixture: &fixture},
		&installFakeRegistry{calls: calls, fixture: &fixture},
		&installFakeConfig{calls: calls, fixture: &fixture},
		&installFakeVersions{selected: fixture.selected},
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func assertEventSubsequence(t *testing.T, events, want []string) {
	t.Helper()
	next := 0
	for _, event := range events {
		if next < len(want) && event == want[next] {
			next++
		}
	}
	if next != len(want) {
		t.Fatalf("events = %v, want subsequence %v", events, want)
	}
}

func assertInstallItems(t *testing.T, result cli.InstallResult, want []cli.InstallItem) {
	t.Helper()
	if result.Items == nil {
		t.Fatal("Install() returned a nil Items slice")
	}
	if !reflect.DeepEqual(result.Items, want) {
		t.Fatalf("Install().Items = %#v, want %#v", result.Items, want)
	}
}

func containsEvent(events []string, want string) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}
