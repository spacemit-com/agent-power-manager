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

type uninstallFixture struct {
	recovery           transaction.RecoveryResult
	snapshot           domain.AgentSnapshot
	groups             map[domain.AgentID]domain.PowerGroups
	transactionResults map[domain.AgentID]transaction.UninstallResult

	acquireErr        error
	recoverErr        error
	refreshErr        error
	releaseErr        error
	configErrors      map[domain.AgentID]error
	resolveErrors     map[domain.AgentID]error
	transactionErrors map[domain.AgentID]error

	beforeRelease func()
}

type uninstallFakeUnlock struct {
	calls   *[]string
	fixture *uninstallFixture
}

func (u *uninstallFakeUnlock) Release() error {
	if u.fixture.beforeRelease != nil {
		u.fixture.beforeRelease()
	}
	*u.calls = append(*u.calls, "release")
	return u.fixture.releaseErr
}

type uninstallFakeLocker struct {
	calls   *[]string
	fixture *uninstallFixture
}

func (l *uninstallFakeLocker) Acquire() (lock.Unlock, error) {
	*l.calls = append(*l.calls, "acquire")
	if l.fixture.acquireErr != nil {
		return nil, l.fixture.acquireErr
	}
	return &uninstallFakeUnlock{calls: l.calls, fixture: l.fixture}, nil
}

type uninstallFakeTransactions struct {
	calls   *[]string
	fixture *uninstallFixture
}

func (m *uninstallFakeTransactions) Recover(context.Context) (transaction.RecoveryResult, error) {
	*m.calls = append(*m.calls, "recover")
	return m.fixture.recovery, m.fixture.recoverErr
}

func (*uninstallFakeTransactions) Install(context.Context, transaction.InstallRequest) error {
	return nil
}

func (m *uninstallFakeTransactions) Uninstall(_ context.Context, request transaction.UninstallRequest) (transaction.UninstallResult, error) {
	*m.calls = append(*m.calls, "transaction:uninstall:"+string(request.Agent))
	return m.fixture.transactionResults[request.Agent], m.fixture.transactionErrors[request.Agent]
}

type uninstallFakeRegistry struct {
	calls   *[]string
	fixture *uninstallFixture
}

func (r *uninstallFakeRegistry) Resolve(agent domain.AgentID, _ domain.PowerType) (adapter.Adapter, error) {
	*r.calls = append(*r.calls, "resolve:"+string(agent))
	return uninstallFakeAdapter{}, r.fixture.resolveErrors[agent]
}

func (r *uninstallFakeRegistry) RefreshInstalled() (domain.AgentSnapshot, error) {
	*r.calls = append(*r.calls, "refresh")
	return r.fixture.snapshot, r.fixture.refreshErr
}

type uninstallFakeAdapter struct{}

func (uninstallFakeAdapter) DetectAgent() (bool, error) { return true, nil }

func (uninstallFakeAdapter) TargetPath(domain.PowerRef) (string, error) { return "/target", nil }

func (uninstallFakeAdapter) Install(domain.PowerRef, string) (adapter.InstallResult, error) {
	return adapter.InstallResult{Published: true}, nil
}

type uninstallFakeConfig struct {
	calls   *[]string
	fixture *uninstallFixture
}

func (c *uninstallFakeConfig) List(agent domain.AgentID) (domain.PowerGroups, error) {
	*c.calls = append(*c.calls, "config:"+string(agent))
	return c.fixture.groups[agent], c.fixture.configErrors[agent]
}

func (*uninstallFakeConfig) Find(domain.AgentID, domain.PowerType, string) (domain.PowerRef, bool, error) {
	return domain.PowerRef{}, false, nil
}

func (*uninstallFakeConfig) Set(domain.AgentID, domain.PowerRef) error { return nil }

func (*uninstallFakeConfig) Remove(domain.AgentID, domain.PowerType, string) error { return nil }

func TestNewUninstallRejectsNilDependencies(t *testing.T) {
	var calls []string
	fixture := &uninstallFixture{}
	locker := &uninstallFakeLocker{calls: &calls, fixture: fixture}
	transactions := &uninstallFakeTransactions{calls: &calls, fixture: fixture}
	registry := &uninstallFakeRegistry{calls: &calls, fixture: fixture}
	config := &uninstallFakeConfig{calls: &calls, fixture: fixture}

	tests := []struct {
		name string
		new  func() (*UninstallService, error)
		want string
	}{
		{name: "locker", new: func() (*UninstallService, error) { return NewUninstall(nil, transactions, registry, config) }, want: "create Uninstall lifecycle with nil locker"},
		{name: "typed nil locker", new: func() (*UninstallService, error) {
			return NewUninstall((*uninstallFakeLocker)(nil), transactions, registry, config)
		}, want: "create Uninstall lifecycle with nil locker"},
		{name: "transaction manager", new: func() (*UninstallService, error) { return NewUninstall(locker, nil, registry, config) }, want: "create Uninstall lifecycle with nil transaction manager"},
		{name: "typed nil transaction manager", new: func() (*UninstallService, error) {
			return NewUninstall(locker, (*uninstallFakeTransactions)(nil), registry, config)
		}, want: "create Uninstall lifecycle with nil transaction manager"},
		{name: "adapter registry", new: func() (*UninstallService, error) { return NewUninstall(locker, transactions, nil, config) }, want: "create Uninstall lifecycle with nil adapter registry"},
		{name: "typed nil adapter registry", new: func() (*UninstallService, error) {
			return NewUninstall(locker, transactions, (*uninstallFakeRegistry)(nil), config)
		}, want: "create Uninstall lifecycle with nil adapter registry"},
		{name: "config repository", new: func() (*UninstallService, error) { return NewUninstall(locker, transactions, registry, nil) }, want: "create Uninstall lifecycle with nil config repository"},
		{name: "typed nil config repository", new: func() (*UninstallService, error) {
			return NewUninstall(locker, transactions, registry, (*uninstallFakeConfig)(nil))
		}, want: "create Uninstall lifecycle with nil config repository"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := tt.new()
			if service != nil || err == nil || err.Error() != tt.want {
				t.Fatalf("NewUninstall() = (%v, %v), want (nil, %q)", service, err, tt.want)
			}
		})
	}
}

func TestUninstallAllUsesSnapshotOrderAndDistinctPowerPointers(t *testing.T) {
	claudeRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
	codexRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}
	var calls []string
	service := newUninstallService(t, &calls, uninstallFixture{
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentClaudeCode, domain.AgentCodex}},
		groups: map[domain.AgentID]domain.PowerGroups{
			domain.AgentClaudeCode: {domain.PowerTypePlugin: {claudeRef}},
			domain.AgentCodex:      {domain.PowerTypeSkill: {codexRef}},
		},
	})

	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: "all", Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	wantEvents := []string{
		"acquire", "recover", "refresh",
		"config:claude-code", "resolve:claude-code", "transaction:uninstall:claude-code",
		"config:codex", "resolve:codex", "transaction:uninstall:codex",
		"release",
	}
	assertEventSubsequence(t, calls, wantEvents)
	assertUninstallItems(t, result, []cli.UninstallItem{
		{Agent: domain.AgentClaudeCode, Power: powerPointer(claudeRef), Outcome: cli.UninstallOutcomeCompleted},
		{Agent: domain.AgentCodex, Power: powerPointer(codexRef), Outcome: cli.UninstallOutcomeCompleted},
	})
	if result.Items[0].Power == result.Items[1].Power {
		t.Fatal("Uninstall() reused a Power pointer across Agent results")
	}
}

func TestUninstallConfigFailureReturnsAgentOnlyFailedAndLaterUnexecutedItems(t *testing.T) {
	cause := errors.New("read manifest")
	var calls []string
	service := newUninstallService(t, &calls, uninstallFixture{
		snapshot:     domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentClaudeCode, domain.AgentCodex}},
		configErrors: map[domain.AgentID]error{domain.AgentClaudeCode: cause},
	})

	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: "all", Name: "demo"})
	assertUninstallItems(t, result, []cli.UninstallItem{
		{Agent: domain.AgentClaudeCode, Outcome: cli.UninstallOutcomeFailed},
		{Agent: domain.AgentCodex, Outcome: cli.UninstallOutcomeUnexecuted},
	})
	assertUninstallBatchError(t, err, domain.AgentClaudeCode, cause)
	wantCalls := []string{"acquire", "recover", "refresh", "config:claude-code", "release"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
}

func TestUninstallPostResolutionFailuresReturnFullPowerFailedItems(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*uninstallFixture, error)
	}{
		{name: "Adapter Resolve", configure: func(f *uninstallFixture, cause error) {
			f.resolveErrors = map[domain.AgentID]error{domain.AgentCodex: cause}
		}},
		{name: "Transaction Uninstall", configure: func(f *uninstallFixture, cause error) {
			f.transactionErrors = map[domain.AgentID]error{domain.AgentCodex: cause}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cause := errors.New(tt.name)
			fixture := uninstallTargetFixture()
			tt.configure(&fixture, cause)
			var calls []string
			service := newUninstallService(t, &calls, fixture)

			result, err := service.Uninstall(context.Background(), uninstallTargetFlags("codex"))
			assertUninstallItems(t, result, []cli.UninstallItem{{
				Agent: domain.AgentCodex, Power: powerPointer(uninstallTarget()), Outcome: cli.UninstallOutcomeFailed,
			}})
			assertUninstallBatchError(t, err, domain.AgentCodex, cause)
			if !containsEvent(calls, "release") {
				t.Fatalf("calls = %v, want release", calls)
			}
		})
	}
}

func TestUninstallReleaseFailureSuppressesResultAndBatchError(t *testing.T) {
	businessCause := errors.New("remove target")
	releaseCause := errors.New("unlock filesystem")
	fixture := uninstallTargetFixture()
	fixture.transactionErrors = map[domain.AgentID]error{domain.AgentCodex: businessCause}
	fixture.releaseErr = releaseCause
	var calls []string
	service := newUninstallService(t, &calls, fixture)

	result, err := service.Uninstall(context.Background(), uninstallTargetFlags("codex"))
	assertZeroUninstallResult(t, result)
	assertNoUninstallBatchError(t, err)
	if !errors.Is(err, businessCause) || !errors.Is(err, releaseCause) {
		t.Fatalf("Uninstall() error = %v, want business %v and release %v", err, businessCause, releaseCause)
	}
}

func TestUninstallRecoverFailureReleasesAndStopsBeforeRefresh(t *testing.T) {
	cause := errors.New("inspect journal")
	var calls []string
	service := newUninstallService(t, &calls, uninstallFixture{recoverErr: cause})

	result, err := service.Uninstall(context.Background(), uninstallTargetFlags("codex"))
	assertZeroUninstallResult(t, result)
	assertNoUninstallBatchError(t, err)
	if !errors.Is(err, cause) {
		t.Fatalf("Uninstall() error = %v, want cause %v", err, cause)
	}
	wantCalls := []string{"acquire", "recover", "release"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
}

func TestUninstallRecoveryWarnsSynchronouslyBeforeRelease(t *testing.T) {
	var stdout, stderr bytes.Buffer
	installTestLogger(t, &stdout, &stderr, false)
	fixture := uninstallTargetFixture()
	fixture.recovery = transaction.RecoveryResult{
		Recovered:     true,
		Operation:     transaction.OperationInstall,
		Agent:         domain.AgentCodex,
		Power:         domain.PowerRef{Name: "demo", Version: "v1.0.0"},
		OriginalPhase: transaction.PhasePublishing,
		Direction:     transaction.RecoveryCommit,
		Actions:       []transaction.RecoveryAction{{Kind: transaction.RecoveryActionSetManifest}},
	}
	fixture.beforeRelease = func() {
		assertContainsAll(t, stderr.String(), []string{
			"level=WARN", `msg="transaction recovered"`, "operation=install",
			"agent=codex", "power=demo", "version=v1.0.0",
			"phase=publishing", "direction=commit", "actions=",
		})
	}
	var calls []string
	service := newUninstallService(t, &calls, fixture)

	if _, err := service.Uninstall(context.Background(), uninstallTargetFlags("codex")); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestUninstallNoJournalHasNoNormalOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	installTestLogger(t, &stdout, &stderr, false)
	var calls []string
	service := newUninstallService(t, &calls, uninstallTargetFixture())

	if _, err := service.Uninstall(context.Background(), uninstallTargetFlags("codex")); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("output = stdout %q stderr %q, want no output", stdout.String(), stderr.String())
	}
}

func TestUninstallNoJournalWritesOnlyDebugWhenVerbose(t *testing.T) {
	var stdout, stderr bytes.Buffer
	installTestLogger(t, &stdout, &stderr, true)
	var calls []string
	service := newUninstallService(t, &calls, uninstallTargetFixture())

	if _, err := service.Uninstall(context.Background(), uninstallTargetFlags("codex")); err != nil {
		t.Fatal(err)
	}
	assertContainsAll(t, stderr.String(), []string{"level=DEBUG", `msg="no active transaction before uninstall"`})
	if strings.Contains(stderr.String(), "level=WARN") || strings.Count(stderr.String(), "\n") != 1 {
		t.Fatalf("stderr = %q, want exactly one Debug diagnostic", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestUninstallMissingRecordWarnsSynchronouslyWithOptionalType(t *testing.T) {
	tests := []struct {
		name     string
		flags    cli.Flags
		wantType string
	}{
		{name: "without type", flags: cli.Flags{Agent: "codex", Name: "Demo"}},
		{name: "with type", flags: cli.Flags{Agent: "codex", Type: "plugin", Name: "Demo"}, wantType: "type=plugin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			installTestLogger(t, &stdout, &stderr, false)
			fixture := uninstallFixture{snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}}}
			fixture.beforeRelease = func() {
				assertContainsAll(t, stderr.String(), []string{
					`msg="power is not installed"`, "operation=uninstall", "agent=codex", "power=Demo",
				})
			}
			var calls []string
			service := newUninstallService(t, &calls, fixture)

			result, err := service.Uninstall(context.Background(), tt.flags)
			if err != nil {
				t.Fatal(err)
			}
			assertUninstallItems(t, result, []cli.UninstallItem{{Agent: domain.AgentCodex, Outcome: cli.UninstallOutcomeUnexecuted}})
			if strings.Count(stderr.String(), "level=WARN") != 1 {
				t.Fatalf("stderr = %q, want exactly one Warn", stderr.String())
			}
			if tt.wantType == "" && strings.Contains(stderr.String(), "type=") {
				t.Fatalf("stderr = %q, want no type attribute", stderr.String())
			}
			if tt.wantType != "" && !strings.Contains(stderr.String(), tt.wantType) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.wantType)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestUninstallMissingTargetWarnsSynchronouslyAndCompletes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	installTestLogger(t, &stdout, &stderr, false)
	fixture := uninstallTargetFixture()
	fixture.transactionResults = map[domain.AgentID]transaction.UninstallResult{
		domain.AgentCodex: {TargetMissing: true},
	}
	fixture.beforeRelease = func() {
		assertContainsAll(t, stderr.String(), []string{
			`msg="installed power target is missing"`, "operation=uninstall",
			"agent=codex", "power=demo", "type=skill", "version=v1.2.3",
		})
	}
	var calls []string
	service := newUninstallService(t, &calls, fixture)

	result, err := service.Uninstall(context.Background(), uninstallTargetFlags("codex"))
	if err != nil {
		t.Fatal(err)
	}
	assertUninstallItems(t, result, []cli.UninstallItem{{
		Agent: domain.AgentCodex, Power: powerPointer(uninstallTarget()), Outcome: cli.UninstallOutcomeCompleted,
	}})
	if strings.Count(stderr.String(), "level=WARN") != 1 {
		t.Fatalf("stderr = %q, want exactly one Warn", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func newUninstallService(t *testing.T, calls *[]string, fixture uninstallFixture) *UninstallService {
	t.Helper()
	service, err := NewUninstall(
		&uninstallFakeLocker{calls: calls, fixture: &fixture},
		&uninstallFakeTransactions{calls: calls, fixture: &fixture},
		&uninstallFakeRegistry{calls: calls, fixture: &fixture},
		&uninstallFakeConfig{calls: calls, fixture: &fixture},
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func uninstallTarget() domain.PowerRef {
	return domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.2.3"}
}

func uninstallTargetFixture() uninstallFixture {
	target := uninstallTarget()
	return uninstallFixture{
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		groups: map[domain.AgentID]domain.PowerGroups{
			domain.AgentCodex: {domain.PowerTypeSkill: {target}},
		},
	}
}

func uninstallTargetFlags(agent string) cli.Flags {
	return cli.Flags{Agent: agent, Type: "skill", Name: "demo"}
}

func powerPointer(ref domain.PowerRef) *domain.PowerRef {
	copied := ref
	return &copied
}

func assertUninstallItems(t *testing.T, result cli.UninstallResult, want []cli.UninstallItem) {
	t.Helper()
	if result.Items == nil {
		t.Fatal("Uninstall() returned a nil Items slice")
	}
	if !reflect.DeepEqual(result.Items, want) {
		t.Fatalf("Uninstall().Items = %#v, want %#v", result.Items, want)
	}
}

func assertZeroUninstallResult(t *testing.T, result cli.UninstallResult) {
	t.Helper()
	if result.Items != nil {
		t.Fatalf("Uninstall() result = %#v, want zero result", result)
	}
}

func assertNoUninstallBatchError(t *testing.T, err error) {
	t.Helper()
	var batch *cli.UninstallBatchError
	if errors.As(err, &batch) {
		t.Fatalf("Uninstall() error = %v, want no UninstallBatchError", err)
	}
}

func assertUninstallBatchError(t *testing.T, err error, agent domain.AgentID, cause error) {
	t.Helper()
	var batch *cli.UninstallBatchError
	if !errors.As(err, &batch) {
		t.Fatalf("Uninstall() error = %v, want UninstallBatchError", err)
	}
	if batch.Agent != agent {
		t.Fatalf("UninstallBatchError agent = %q, want %q", batch.Agent, agent)
	}
	if !errors.Is(batch, cause) {
		t.Fatalf("UninstallBatchError = %v, want cause %v", batch, cause)
	}
}

func installTestLogger(t *testing.T, stdout, stderr *bytes.Buffer, verbose bool) {
	t.Helper()
	originalLogger := slog.Default()
	slog.SetDefault(logging.NewLogger(stdout, stderr, verbose))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })
}

func assertContainsAll(t *testing.T, value string, wants []string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(value, want) {
			t.Fatalf("value = %q, want it to contain %q", value, want)
		}
	}
}
