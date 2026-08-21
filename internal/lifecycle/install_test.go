package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/logging"
	"apm/internal/transaction"
)

func TestInstallAllUsesSnapshotOrderAndFetchesSeparatelyPerChangedAgent(t *testing.T) {
	var calls []string
	service := newInstallService(t, &calls, installFixture{
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{
			domain.AgentBibit, domain.AgentClaudeCode, domain.AgentCodex, domain.AgentHermes,
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
		"acquire", "recover", "refresh", "cloud:resolve",
		"resolve:bibit", "config:find:bibit", "cache:stage-skill:bibit", "transaction:install:bibit",
		"resolve:claude-code", "config:find:claude-code", "cache:stage-skill:claude-code", "transaction:install:claude-code",
		"resolve:codex", "config:find:codex",
		"resolve:hermes-agent", "config:find:hermes-agent", "cache:stage-skill:hermes-agent", "transaction:install:hermes-agent",
		"release",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	assertInstallItems(t, result, []cli.InstallItem{
		{Agent: domain.AgentBibit, Power: domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}, Outcome: cli.InstallOutcomeInstalled},
		{Agent: domain.AgentClaudeCode, Power: domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}, Outcome: cli.InstallOutcomeInstalled},
		{Agent: domain.AgentCodex, Power: domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}, Outcome: cli.InstallOutcomeUnchanged},
		{Agent: domain.AgentHermes, Power: domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}, Outcome: cli.InstallOutcomeInstalled},
	})
}

func TestInstallExplicitPluginManifestHitSkipsCloudAndMutablePluginDependencies(t *testing.T) {
	// 捕获错误变更：公共 Install 在 manifest 精确命中前仍先访问 Cloud 或进入资源、Adapter、Transaction。
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v2.0.0"}
	cloudCause := errors.New("Cloud must remain offline on manifest hit")
	agents := []domain.AgentID{domain.AgentBibit, domain.AgentClaudeCode, domain.AgentCodex, domain.AgentHermes}
	installed := make(map[domain.AgentID]domain.PowerRef, len(agents))
	for _, agent := range agents {
		installed[agent] = ref
	}
	var calls []string
	service := newInstallService(t, &calls, installFixture{
		snapshot:     domain.AgentSnapshot{Agents: agents},
		installed:    installed,
		cloudListErr: cloudCause,
	})

	result, err := service.Install(context.Background(), cli.Flags{
		Agent: "all", Type: "plugin", Name: "demo", Version: "v2.0.0",
	})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	assertInstallItems(t, result, []cli.InstallItem{
		{Agent: domain.AgentBibit, Power: ref, Outcome: cli.InstallOutcomeUnchanged},
		{Agent: domain.AgentClaudeCode, Power: ref, Outcome: cli.InstallOutcomeUnchanged},
		{Agent: domain.AgentCodex, Power: ref, Outcome: cli.InstallOutcomeUnchanged},
		{Agent: domain.AgentHermes, Power: ref, Outcome: cli.InstallOutcomeUnchanged},
	})
	wantCalls := []string{
		"acquire", "recover", "refresh",
		"config:find:bibit", "config:find:claude-code", "config:find:codex", "config:find:hermes-agent",
		"release",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want manifest-only %v", calls, wantCalls)
	}
}

func TestInstallExplicitPluginManifestFastPathFallsBackWhenAnyAgentDiffers(t *testing.T) {
	// 捕获错误变更：批量 fast path 只检查部分 Agent，错误地把混合版本批次全部返回 unchanged。
	target := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v2.0.0"}
	cloudCause := errors.New("Cloud fallback reached")
	var calls []string
	service := newInstallService(t, &calls, installFixture{
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex, domain.AgentHermes}},
		installed: map[domain.AgentID]domain.PowerRef{
			domain.AgentCodex:  target,
			domain.AgentHermes: {Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"},
		},
		cloudListErr: cloudCause,
	})

	result, err := service.Install(context.Background(), cli.Flags{
		Agent: "all", Type: "plugin", Name: "demo", Version: "v2.0.0",
	})
	if !errors.Is(err, cloudCause) {
		t.Fatalf("Install() error = %v, want Cloud fallback cause %v", err, cloudCause)
	}
	assertZeroInstallResult(t, result)
	wantCalls := []string{
		"acquire", "recover", "refresh", "config:find:codex", "config:find:hermes-agent", "cloud:resolve", "release",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want fallback %v", calls, wantCalls)
	}
}

func TestInstallPluginFastPathRequiresExplicitCanonicalVersion(t *testing.T) {
	// 捕获错误变更：省略版本时绕过 Cloud 版本选择并误用 manifest 中的任意版本。
	cloudCause := errors.New("Cloud version selection required")
	var calls []string
	service := newInstallService(t, &calls, installFixture{
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		installed: map[domain.AgentID]domain.PowerRef{
			domain.AgentCodex: {Name: "demo", Type: domain.PowerTypePlugin, Version: "v2.0.0"},
		},
		cloudListErr: cloudCause,
	})

	result, err := service.Install(context.Background(), cli.Flags{Agent: "codex", Type: "plugin", Name: "demo"})
	if !errors.Is(err, cloudCause) {
		t.Fatalf("Install() error = %v, want Cloud cause %v", err, cloudCause)
	}
	assertZeroInstallResult(t, result)
	wantCalls := []string{"acquire", "recover", "refresh", "cloud:resolve", "release"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want normal resolution %v", calls, wantCalls)
	}
}

func TestInstallPluginManifestFastPathReleaseFailureSuppressesResult(t *testing.T) {
	// 捕获错误变更：新增 fast path 绕过统一解锁错误抑制，泄露 completed 结果。
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v2.0.0"}
	releaseCause := errors.New("release fast-path lock")
	var calls []string
	service := newInstallService(t, &calls, installFixture{
		snapshot:   domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		installed:  map[domain.AgentID]domain.PowerRef{domain.AgentCodex: ref},
		releaseErr: releaseCause,
	})

	result, err := service.Install(context.Background(), cli.Flags{
		Agent: "codex", Type: "plugin", Name: "demo", Version: "v2.0.0",
	})
	if !errors.Is(err, releaseCause) {
		t.Fatalf("Install() error = %v, want release cause %v", err, releaseCause)
	}
	assertZeroInstallResult(t, result)
	assertNoInstallBatchError(t, err)
	wantCalls := []string{"acquire", "recover", "refresh", "config:find:codex", "release"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
}

func TestInstallAllFailsFastAndCompletesResultAfterRelease(t *testing.T) {
	cause := errors.New("find installed Codex power")
	target := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}
	var calls []string
	service := newInstallService(t, &calls, installFixture{
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{
			domain.AgentBibit, domain.AgentClaudeCode, domain.AgentCodex, domain.AgentHermes,
		}},
		cloudRefs: targetRefs(target),
		selected:  target.Version,
		configFindErrors: map[domain.AgentID]error{
			domain.AgentCodex: cause,
		},
	})

	result, err := service.Install(context.Background(), cli.Flags{Agent: "all", Type: "skill", Name: "demo"})
	if err == nil {
		t.Fatal("Install() error = nil, want failed Agent marker")
	}
	assertInstallItems(t, result, []cli.InstallItem{
		{Agent: domain.AgentBibit, Power: target, Outcome: cli.InstallOutcomeInstalled},
		{Agent: domain.AgentClaudeCode, Power: target, Outcome: cli.InstallOutcomeInstalled},
		{Agent: domain.AgentCodex, Power: target, Outcome: cli.InstallOutcomeFailed},
		{Agent: domain.AgentHermes, Power: target, Outcome: cli.InstallOutcomeUnexecuted},
	})
	assertInstallBatchError(t, err, domain.AgentCodex, cause)
	if !containsEvent(calls, "release") {
		t.Fatalf("calls = %v, want release", calls)
	}
	for _, event := range calls {
		if strings.Contains(event, string(domain.AgentHermes)) {
			t.Fatalf("calls = %v, want no Hermes operation after Codex failure", calls)
		}
	}
}

func TestInstallFatalPreTargetFailuresReturnZeroResultWithoutBatchError(t *testing.T) {
	tests := []struct {
		name        string
		flags       cli.Flags
		configure   func(*installFixture, error)
		wantRelease bool
		wantCause   bool
	}{
		{name: "acquire", flags: installTargetFlags("codex"), configure: func(f *installFixture, cause error) { f.acquireErr = cause }, wantCause: true},
		{name: "recover", flags: installTargetFlags("codex"), configure: func(f *installFixture, cause error) { f.recoverErr = cause }, wantRelease: true, wantCause: true},
		{name: "refresh", flags: installTargetFlags("codex"), configure: func(f *installFixture, cause error) { f.refreshErr = cause }, wantRelease: true, wantCause: true},
		{name: "concrete Agent missing", flags: installTargetFlags("hermes-agent"), configure: func(*installFixture, error) {}, wantRelease: true},
		{name: "all snapshot empty", flags: installTargetFlags("all"), configure: func(f *installFixture, _ error) { f.snapshot = domain.AgentSnapshot{} }, wantRelease: true},
		{name: "Cloud list", flags: installTargetFlags("codex"), configure: func(f *installFixture, cause error) { f.cloudListErr = cause }, wantRelease: true, wantCause: true},
		{name: "name not found", flags: installTargetFlags("codex"), configure: func(f *installFixture, _ error) {
			f.cloudRefs = targetRefs(domain.PowerRef{Name: "other", Type: domain.PowerTypeSkill, Version: "v2.0.0"})
		}, wantRelease: true},
		{name: "type ambiguity", flags: cli.Flags{Agent: "codex", Name: "demo"}, configure: func(f *installFixture, _ error) {
			f.cloudRefs = []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}, {Name: "demo", Type: domain.PowerTypePlugin, Version: "v2.0.0"}}
		}, wantRelease: true},
		{name: "explicit type not found", flags: cli.Flags{Agent: "codex", Type: "mcp", Name: "demo"}, configure: func(*installFixture, error) {}, wantRelease: true},
		{name: "version not found", flags: cli.Flags{Agent: "codex", Type: "skill", Name: "demo", Version: "v9.0.0"}, configure: func(f *installFixture, cause error) { f.versionErr = cause }, wantRelease: true, wantCause: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cause := errors.New(tt.name)
			fixture := installTargetFixture()
			tt.configure(&fixture, cause)
			var calls []string
			service := newInstallService(t, &calls, fixture)

			result, err := service.Install(context.Background(), tt.flags)
			if err == nil {
				t.Fatal("Install() error = nil, want fatal failure")
			}
			assertZeroInstallResult(t, result)
			assertNoInstallBatchError(t, err)
			if tt.wantCause && !errors.Is(err, cause) {
				t.Fatalf("Install() error = %v, want cause %v", err, cause)
			}
			if containsEvent(calls, "release") != tt.wantRelease {
				t.Fatalf("calls = %v, release = %t, want %t", calls, containsEvent(calls, "release"), tt.wantRelease)
			}
		})
	}
}

func TestInstallRecoveryFailureStillReleasesAndJoinsReleaseError(t *testing.T) {
	businessCause := errors.New("recover transaction")
	releaseCause := errors.New("unlock filesystem")
	fixture := installTargetFixture()
	fixture.recoverErr = businessCause
	fixture.releaseErr = releaseCause
	var calls []string
	service := newInstallService(t, &calls, fixture)

	result, err := service.Install(context.Background(), installTargetFlags("codex"))
	if err == nil {
		t.Fatal("Install() error = nil, want recovery and release failure")
	}
	assertZeroInstallResult(t, result)
	assertNoInstallBatchError(t, err)
	if !errors.Is(err, businessCause) || !errors.Is(err, releaseCause) {
		t.Fatalf("Install() error = %v, want recovery %v and release %v", err, businessCause, releaseCause)
	}
	if !containsEvent(calls, "release") {
		t.Fatalf("calls = %v, want release", calls)
	}
}

func TestInstallFatalBusinessAndReleaseFailuresAreJoinedWithoutResult(t *testing.T) {
	businessCause := errors.New("list Cloud powers")
	releaseCause := errors.New("unlock filesystem")
	fixture := installTargetFixture()
	fixture.cloudListErr = businessCause
	fixture.releaseErr = releaseCause
	var calls []string
	service := newInstallService(t, &calls, fixture)

	result, err := service.Install(context.Background(), installTargetFlags("codex"))
	if err == nil {
		t.Fatal("Install() error = nil, want Cloud list and release failure")
	}
	assertZeroInstallResult(t, result)
	assertNoInstallBatchError(t, err)
	if !errors.Is(err, businessCause) || !errors.Is(err, releaseCause) {
		t.Fatalf("Install() error = %v, want list %v and release %v", err, businessCause, releaseCause)
	}
}

func TestInstallBatchAndReleaseFailuresSuppressResultAndMarker(t *testing.T) {
	businessCause := errors.New("find installed power")
	releaseCause := errors.New("unlock filesystem")
	fixture := installTargetFixture()
	fixture.configFindErrors = map[domain.AgentID]error{domain.AgentCodex: businessCause}
	fixture.releaseErr = releaseCause
	var calls []string
	service := newInstallService(t, &calls, fixture)

	result, err := service.Install(context.Background(), installTargetFlags("codex"))
	if err == nil {
		t.Fatal("Install() error = nil, want Agent and release failure")
	}
	assertZeroInstallResult(t, result)
	assertNoInstallBatchError(t, err)
	if !errors.Is(err, businessCause) || !errors.Is(err, releaseCause) {
		t.Fatalf("Install() error = %v, want Agent %v and release %v", err, businessCause, releaseCause)
	}
}

func TestInstallReleaseFailureAfterSuccessSuppressesCompletedResult(t *testing.T) {
	releaseCause := errors.New("unlock filesystem")
	fixture := installTargetFixture()
	fixture.releaseErr = releaseCause
	var calls []string
	service := newInstallService(t, &calls, fixture)

	result, err := service.Install(context.Background(), installTargetFlags("codex"))
	if err == nil {
		t.Fatal("Install() error = nil, want release failure")
	}
	assertZeroInstallResult(t, result)
	assertNoInstallBatchError(t, err)
	if !errors.Is(err, releaseCause) {
		t.Fatalf("Install() error = %v, want release cause %v", err, releaseCause)
	}
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
	pluginResources := &installFakePluginResources{}
	registry := &installFakeRegistry{calls: &calls, fixture: fixture}
	config := &installFakeConfig{calls: &calls, fixture: fixture}
	versions := &installFakeVersions{}

	tests := []struct {
		name string
		new  func() (*InstallService, error)
		want string
	}{
		{name: "locker", new: func() (*InstallService, error) {
			return NewInstall(nil, transactions, cloud, cache, pluginResources, registry, config, versions)
		}, want: "create Install lifecycle with nil locker"},
		{name: "typed nil locker", new: func() (*InstallService, error) {
			return NewInstall((*installFakeLocker)(nil), transactions, cloud, cache, pluginResources, registry, config, versions)
		}, want: "create Install lifecycle with nil locker"},
		{name: "transaction manager", new: func() (*InstallService, error) {
			return NewInstall(locker, nil, cloud, cache, pluginResources, registry, config, versions)
		}, want: "create Install lifecycle with nil transaction manager"},
		{name: "typed nil transaction manager", new: func() (*InstallService, error) {
			return NewInstall(locker, (*installFakeTransactions)(nil), cloud, cache, pluginResources, registry, config, versions)
		}, want: "create Install lifecycle with nil transaction manager"},
		{name: "Cloud client", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, nil, cache, pluginResources, registry, config, versions)
		}, want: "create Install lifecycle with nil Cloud client"},
		{name: "typed nil Cloud client", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, (*installFakeCloud)(nil), cache, pluginResources, registry, config, versions)
		}, want: "create Install lifecycle with nil Cloud client"},
		{name: "power cache", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, cloud, nil, pluginResources, registry, config, versions)
		}, want: "create Install lifecycle with nil power cache"},
		{name: "typed nil power cache", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, cloud, (*installFakeCache)(nil), pluginResources, registry, config, versions)
		}, want: "create Install lifecycle with nil power cache"},
		{name: "Plugin Resource manager", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, cloud, cache, nil, registry, config, versions)
		}, want: "create Install lifecycle with nil Plugin Resource manager"},
		{name: "typed nil Plugin Resource manager", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, cloud, cache, (*installFakePluginResources)(nil), registry, config, versions)
		}, want: "create Install lifecycle with nil Plugin Resource manager"},
		{name: "adapter registry", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, cloud, cache, pluginResources, nil, config, versions)
		}, want: "create Install lifecycle with nil adapter registry"},
		{name: "typed nil adapter registry", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, cloud, cache, pluginResources, (*installFakeRegistry)(nil), config, versions)
		}, want: "create Install lifecycle with nil adapter registry"},
		{name: "config repository", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, cloud, cache, pluginResources, registry, nil, versions)
		}, want: "create Install lifecycle with nil config repository"},
		{name: "typed nil config repository", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, cloud, cache, pluginResources, registry, (*installFakeConfig)(nil), versions)
		}, want: "create Install lifecycle with nil config repository"},
		{name: "version selector", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, cloud, cache, pluginResources, registry, config, nil)
		}, want: "create Install lifecycle with nil version selector"},
		{name: "typed nil version selector", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, cloud, cache, pluginResources, registry, config, (*installFakeVersions)(nil))
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
