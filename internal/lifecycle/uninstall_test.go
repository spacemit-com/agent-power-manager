package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"
)

func TestNewUninstallRejectsNilDependencies(t *testing.T) {
	var calls []string
	fixture := &uninstallFixture{}
	locker := &uninstallFakeLocker{calls: &calls, fixture: fixture}
	transactions := &uninstallFakeTransactions{calls: &calls, fixture: fixture}
	pluginResources := &installFakePluginResources{}
	registry := &uninstallFakeRegistry{calls: &calls, fixture: fixture}
	config := &uninstallFakeConfig{calls: &calls, fixture: fixture}

	tests := []struct {
		name string
		new  func() (*UninstallService, error)
		want string
	}{
		{name: "locker", new: func() (*UninstallService, error) {
			return NewUninstall(nil, transactions, pluginResources, registry, config)
		}, want: "create Uninstall lifecycle with nil locker"},
		{name: "typed nil locker", new: func() (*UninstallService, error) {
			return NewUninstall((*uninstallFakeLocker)(nil), transactions, pluginResources, registry, config)
		}, want: "create Uninstall lifecycle with nil locker"},
		{name: "transaction manager", new: func() (*UninstallService, error) {
			return NewUninstall(locker, nil, pluginResources, registry, config)
		}, want: "create Uninstall lifecycle with nil transaction manager"},
		{name: "typed nil transaction manager", new: func() (*UninstallService, error) {
			return NewUninstall(locker, (*uninstallFakeTransactions)(nil), pluginResources, registry, config)
		}, want: "create Uninstall lifecycle with nil transaction manager"},
		{name: "Plugin Resource manager", new: func() (*UninstallService, error) {
			return NewUninstall(locker, transactions, nil, registry, config)
		}, want: "create Uninstall lifecycle with nil Plugin Resource manager"},
		{name: "typed nil Plugin Resource manager", new: func() (*UninstallService, error) {
			return NewUninstall(locker, transactions, (*installFakePluginResources)(nil), registry, config)
		}, want: "create Uninstall lifecycle with nil Plugin Resource manager"},
		{name: "adapter registry", new: func() (*UninstallService, error) {
			return NewUninstall(locker, transactions, pluginResources, nil, config)
		}, want: "create Uninstall lifecycle with nil adapter registry"},
		{name: "typed nil adapter registry", new: func() (*UninstallService, error) {
			return NewUninstall(locker, transactions, pluginResources, (*uninstallFakeRegistry)(nil), config)
		}, want: "create Uninstall lifecycle with nil adapter registry"},
		{name: "config repository", new: func() (*UninstallService, error) {
			return NewUninstall(locker, transactions, pluginResources, registry, nil)
		}, want: "create Uninstall lifecycle with nil config repository"},
		{name: "typed nil config repository", new: func() (*UninstallService, error) {
			return NewUninstall(locker, transactions, pluginResources, registry, (*uninstallFakeConfig)(nil))
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
	bibitRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v0.9.0"}
	claudeRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}
	codexRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}
	hermesRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v3.0.0"}
	var calls []string
	service := newUninstallService(t, &calls, uninstallFixture{
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentBibit, domain.AgentClaudeCode, domain.AgentCodex, domain.AgentHermes}},
		groups: map[domain.AgentID]domain.InstalledPowerGroups{
			domain.AgentBibit:      {domain.PowerTypeSkill: {domain.InstalledPower{Ref: bibitRef}}},
			domain.AgentClaudeCode: {domain.PowerTypeSkill: {domain.InstalledPower{Ref: claudeRef}}},
			domain.AgentCodex:      {domain.PowerTypeSkill: {domain.InstalledPower{Ref: codexRef}}},
			domain.AgentHermes:     {domain.PowerTypeSkill: {domain.InstalledPower{Ref: hermesRef}}},
		},
	})

	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: "all", Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	wantEvents := []string{
		"acquire", "recover", "refresh",
		"config:bibit", "resolve:bibit", "transaction:uninstall:bibit",
		"config:claude-code", "resolve:claude-code", "transaction:uninstall:claude-code",
		"config:codex", "resolve:codex", "transaction:uninstall:codex",
		"config:hermes-agent", "resolve:hermes-agent", "transaction:uninstall:hermes-agent",
		"release",
	}
	assertEventSubsequence(t, calls, wantEvents)
	assertUninstallItems(t, result, []cli.UninstallItem{
		{Agent: domain.AgentBibit, Power: powerPointer(bibitRef), Outcome: cli.UninstallOutcomeCompleted},
		{Agent: domain.AgentClaudeCode, Power: powerPointer(claudeRef), Outcome: cli.UninstallOutcomeCompleted},
		{Agent: domain.AgentCodex, Power: powerPointer(codexRef), Outcome: cli.UninstallOutcomeCompleted},
		{Agent: domain.AgentHermes, Power: powerPointer(hermesRef), Outcome: cli.UninstallOutcomeCompleted},
	})
	seen := make(map[*domain.PowerRef]struct{}, len(result.Items))
	for _, item := range result.Items {
		if _, duplicate := seen[item.Power]; duplicate {
			t.Fatal("Uninstall() reused a Power pointer across Agent results")
		}
		seen[item.Power] = struct{}{}
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
	uninstallTestLogger(t, &stdout, &stderr, false)
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
	uninstallTestLogger(t, &stdout, &stderr, false)
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
	uninstallTestLogger(t, &stdout, &stderr, true)
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

func TestUninstallAllConfigFailurePreventsLaterAgentLifecycleAccess(t *testing.T) {
	cause := errors.New("read manifest")
	laterAgents := []domain.AgentID{domain.AgentCodex, domain.AgentHermes}
	fixture := uninstallFixture{
		snapshot:     domain.AgentSnapshot{Agents: append([]domain.AgentID{domain.AgentClaudeCode}, laterAgents...)},
		configErrors: map[domain.AgentID]error{domain.AgentClaudeCode: cause},
	}
	fixture.onEvent = rejectLaterUninstallEvents(t, laterAgents)
	var calls []string
	service := newUninstallService(t, &calls, fixture)

	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: "all", Name: "demo"})

	assertUninstallItems(t, result, []cli.UninstallItem{
		{Agent: domain.AgentClaudeCode, Outcome: cli.UninstallOutcomeFailed},
		{Agent: domain.AgentCodex, Outcome: cli.UninstallOutcomeUnexecuted},
		{Agent: domain.AgentHermes, Outcome: cli.UninstallOutcomeUnexecuted},
	})
	assertUninstallBatchError(t, err, domain.AgentClaudeCode, cause)
	if !reflect.DeepEqual(calls, []string{"acquire", "recover", "refresh", "config:claude-code", "release"}) {
		t.Fatalf("calls = %v, want no Resolve or Transaction after Config failure", calls)
	}
}

func TestUninstallAllSameAgentTypeAmbiguityPreventsLaterAgentLifecycleAccess(t *testing.T) {
	groups := domain.InstalledPowerGroups{
		domain.PowerTypeSkill:  {{Ref: domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}}},
		domain.PowerTypePlugin: {{Ref: domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}}},
	}
	fixture := uninstallFixture{
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentClaudeCode, domain.AgentCodex}},
		groups:   map[domain.AgentID]domain.InstalledPowerGroups{domain.AgentClaudeCode: groups},
	}
	fixture.onEvent = rejectLaterUninstallEvents(t, []domain.AgentID{domain.AgentCodex})
	var calls []string
	service := newUninstallService(t, &calls, fixture)

	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: "all", Name: "demo"})

	assertUninstallItems(t, result, []cli.UninstallItem{
		{Agent: domain.AgentClaudeCode, Outcome: cli.UninstallOutcomeFailed},
		{Agent: domain.AgentCodex, Outcome: cli.UninstallOutcomeUnexecuted},
	})
	if err == nil || !strings.Contains(err.Error(), "specify --type") {
		t.Fatalf("Uninstall() error = %v, want same-Agent type ambiguity with specify --type", err)
	}
	var batch *cli.UninstallBatchError
	if !errors.As(err, &batch) || batch.Agent != domain.AgentClaudeCode {
		t.Fatalf("Uninstall() error = %v, want batch error for %q", err, domain.AgentClaudeCode)
	}
	if !reflect.DeepEqual(calls, []string{"acquire", "recover", "refresh", "config:claude-code", "release"}) {
		t.Fatalf("calls = %v, want no Resolve or Transaction after ambiguity", calls)
	}
}

func TestUninstallAllMissingThenSuccessfulRecordsPreserveSnapshotOrder(t *testing.T) {
	ref := uninstallTarget()
	fixture := uninstallFixture{
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentClaudeCode, domain.AgentCodex}},
		groups: map[domain.AgentID]domain.InstalledPowerGroups{
			domain.AgentCodex: {domain.PowerTypeSkill: {domain.InstalledPower{Ref: ref}}},
		},
	}
	var calls []string
	service := newUninstallService(t, &calls, fixture)

	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: "all", Type: "skill", Name: "demo"})

	if err != nil {
		t.Fatal(err)
	}
	assertUninstallItems(t, result, []cli.UninstallItem{
		{Agent: domain.AgentClaudeCode, Outcome: cli.UninstallOutcomeUnexecuted},
		{Agent: domain.AgentCodex, Power: powerPointer(ref), Outcome: cli.UninstallOutcomeCompleted},
	})
}

func TestUninstallAllMissingRecordsReturnOnlyUnexecutedItems(t *testing.T) {
	fixture := uninstallFixture{snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentClaudeCode, domain.AgentCodex}}}
	var calls []string
	service := newUninstallService(t, &calls, fixture)

	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: "all", Name: "demo"})

	if err != nil {
		t.Fatal(err)
	}
	assertUninstallItems(t, result, []cli.UninstallItem{
		{Agent: domain.AgentClaudeCode, Outcome: cli.UninstallOutcomeUnexecuted},
		{Agent: domain.AgentCodex, Outcome: cli.UninstallOutcomeUnexecuted},
	})
}

func TestUninstallDoesNotMutateAgentSnapshotAcrossSuccessAndFailure(t *testing.T) {
	tests := []struct {
		name    string
		fixture uninstallFixture
		flags   cli.Flags
	}{
		{
			name: "success",
			fixture: uninstallFixture{groups: map[domain.AgentID]domain.InstalledPowerGroups{
				domain.AgentClaudeCode: {domain.PowerTypeSkill: {domain.InstalledPower{Ref: uninstallTarget()}}},
				domain.AgentCodex:      {domain.PowerTypeSkill: {domain.InstalledPower{Ref: uninstallTarget()}}},
			}},
			flags: cli.Flags{Agent: "all", Type: "skill", Name: "demo"},
		},
		{
			name:    "first Agent failure",
			fixture: uninstallFixture{configErrors: map[domain.AgentID]error{domain.AgentClaudeCode: errors.New("read manifest")}},
			flags:   cli.Flags{Agent: "all", Name: "demo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentClaudeCode, domain.AgentCodex}}
			wantSnapshot := domain.AgentSnapshot{Agents: append([]domain.AgentID(nil), snapshot.Agents...)}
			tt.fixture.snapshot = snapshot
			var calls []string
			service := newUninstallService(t, &calls, tt.fixture)

			_, _ = service.Uninstall(context.Background(), tt.flags)

			if !reflect.DeepEqual(snapshot, wantSnapshot) {
				t.Fatalf("Uninstall() mutated AgentSnapshot: got %#v, want %#v", snapshot, wantSnapshot)
			}
		})
	}
}

func TestUninstallRecoverAndReleaseFailuresPreserveBothCauses(t *testing.T) {
	recoverCause := errors.New("inspect journal")
	releaseCause := errors.New("unlock filesystem")
	var calls []string
	service := newUninstallService(t, &calls, uninstallFixture{recoverErr: recoverCause, releaseErr: releaseCause})

	result, err := service.Uninstall(context.Background(), uninstallTargetFlags("codex"))

	assertZeroUninstallResult(t, result)
	assertNoUninstallBatchError(t, err)
	if !errors.Is(err, recoverCause) || !errors.Is(err, releaseCause) {
		t.Fatalf("Uninstall() error = %v, want recover %v and release %v", err, recoverCause, releaseCause)
	}
	if !reflect.DeepEqual(calls, []string{"acquire", "recover", "release"}) {
		t.Fatalf("calls = %v, want one Release and no Refresh", calls)
	}
}

func TestUninstallFatalPreBatchFailuresSuppressResultsAndBatchMarker(t *testing.T) {
	refreshCause := errors.New("refresh agents")
	tests := []struct {
		name    string
		fixture uninstallFixture
		flags   cli.Flags
	}{
		{
			name:    "Refresh failure",
			fixture: uninstallFixture{refreshErr: refreshCause},
			flags:   uninstallTargetFlags("codex"),
		},
		{
			name:    "unsupported concrete Agent",
			fixture: uninstallFixture{snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}}},
			flags:   uninstallTargetFlags("claude-code"),
		},
		{
			name:    "empty all snapshot",
			fixture: uninstallFixture{},
			flags:   cli.Flags{Agent: "all", Name: "demo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			service := newUninstallService(t, &calls, tt.fixture)

			result, err := service.Uninstall(context.Background(), tt.flags)

			assertZeroUninstallResult(t, result)
			assertNoUninstallBatchError(t, err)
			if err == nil {
				t.Fatal("Uninstall() error = nil, want fatal pre-batch error")
			}
			if !containsEvent(calls, "release") {
				t.Fatalf("calls = %v, want Release after fatal pre-batch error", calls)
			}
		})
	}
}

func TestUninstallFactWarningsRemainVisibleWhenReleaseSuppressesResult(t *testing.T) {
	releaseCause := errors.New("unlock filesystem")
	tests := []struct {
		name    string
		fixture uninstallFixture
		flags   cli.Flags
		warning string
	}{
		{
			name:    "missing record",
			fixture: uninstallFixture{releaseErr: releaseCause, snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}}},
			flags:   cli.Flags{Agent: "codex", Name: "demo"},
			warning: `msg="power is not installed"`,
		},
		{
			name: "missing target",
			fixture: func() uninstallFixture {
				fixture := uninstallTargetFixture()
				fixture.releaseErr = releaseCause
				fixture.transactionResults = map[domain.AgentID]transaction.UninstallResult{domain.AgentCodex: {TargetMissing: true}}
				return fixture
			}(),
			flags:   uninstallTargetFlags("codex"),
			warning: `msg="installed power target is missing"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			uninstallTestLogger(t, &stdout, &stderr, false)
			var calls []string
			service := newUninstallService(t, &calls, tt.fixture)

			result, err := service.Uninstall(context.Background(), tt.flags)

			assertZeroUninstallResult(t, result)
			assertNoUninstallBatchError(t, err)
			if !errors.Is(err, releaseCause) {
				t.Fatalf("Uninstall() error = %v, want release cause %v", err, releaseCause)
			}
			if !strings.Contains(stderr.String(), tt.warning) {
				t.Fatalf("stderr = %q, want fact warning %q after Release failure", stderr.String(), tt.warning)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestUninstallRecoveryAndBusinessWarningsAreBothEmittedBeforeRelease(t *testing.T) {
	var stdout, stderr bytes.Buffer
	uninstallTestLogger(t, &stdout, &stderr, false)
	fixture := uninstallFixture{
		recovery: transaction.RecoveryResult{
			Recovered:     true,
			Operation:     transaction.OperationInstall,
			Agent:         domain.AgentCodex,
			Power:         domain.PowerRef{Name: "interrupted", Type: domain.PowerTypeSkill, Version: "v1.0.0"},
			OriginalPhase: transaction.PhasePublishing,
			Direction:     transaction.RecoveryCommit,
		},
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
	}
	fixture.beforeRelease = func() {
		assertContainsAll(t, stderr.String(), []string{
			`msg="transaction recovered"`, "operation=install", "agent=codex", "power=interrupted",
			`msg="power is not installed"`, "operation=uninstall", "power=demo",
		})
		if strings.Count(stderr.String(), "level=WARN") != 2 {
			t.Fatalf("stderr = %q, want recovery and business Warns before Release", stderr.String())
		}
	}
	var calls []string
	service := newUninstallService(t, &calls, fixture)

	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: "codex", Name: "demo"})

	if err != nil {
		t.Fatal(err)
	}
	assertUninstallItems(t, result, []cli.UninstallItem{{Agent: domain.AgentCodex, Outcome: cli.UninstallOutcomeUnexecuted}})
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}
