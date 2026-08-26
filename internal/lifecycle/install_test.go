package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"apm/internal/adapter"
	"apm/internal/cache"
	"apm/internal/catalog"
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
		"acquire", "recover", "refresh", "catalog:snapshot-locked",
		"resolve:bibit", "config:find:bibit", "transaction:inspect-target:bibit:skill", "cache:prepare-skill:bibit", "transaction:install:bibit",
		"resolve:claude-code", "config:find:claude-code", "transaction:inspect-target:claude-code:skill", "cache:prepare-skill:claude-code", "transaction:install:claude-code",
		"resolve:codex", "config:find:codex",
		"resolve:hermes-agent", "config:find:hermes-agent", "transaction:inspect-target:hermes-agent:skill", "cache:prepare-skill:hermes-agent", "transaction:install:hermes-agent",
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

func TestInstallPropagatesResolvedCatalogEntryToTypeBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		powerType domain.PowerType
		agent     domain.AgentID
		configure func(*installFixture)
		entries   func(*InstallService) []domain.CatalogEntry
	}{
		{
			name: "Skill to cache", powerType: domain.PowerTypeSkill, agent: domain.AgentCodex,
			entries: func(service *InstallService) []domain.CatalogEntry {
				return service.cache.(*installFakeCache).fixture.cacheEntries
			},
		},
		{
			name: "Agent to cache", powerType: domain.PowerTypeAgent, agent: domain.AgentBibit,
			entries: func(service *InstallService) []domain.CatalogEntry {
				return service.cache.(*installFakeCache).fixture.cacheEntries
			},
		},
		{
			name: "Plugin to resource manager", powerType: domain.PowerTypePlugin, agent: domain.AgentCodex,
			configure: func(fixture *installFixture) {
				fixture.pluginMode = domain.PluginDeploymentMarketplace
			},
			entries: func(service *InstallService) []domain.CatalogEntry {
				return service.plugins.(*installFakePluginResources).ensureEntries
			},
		},
		{
			name: "MCP to content source", powerType: domain.PowerTypeMCP, agent: domain.AgentCodex,
			configure: func(fixture *installFixture) {
				fixture.mcpPresence = map[domain.AgentID]adapter.MCPPresence{domain.AgentCodex: adapter.MCPAbsent}
				fixture.mcpDefinitions = map[string]domain.MCPServer{"v2.0.0": {Transport: domain.MCPTransportStdio, Command: "demo"}}
			},
			entries: func(service *InstallService) []domain.CatalogEntry {
				return service.cloud.(*installFakeCloud).fixture.cloudEntries
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ref := domain.PowerRef{Name: "demo", Type: test.powerType, Version: "v2.0.0"}
			entry := catalogEntryForInstallTest(ref, "authoritative catalog message")
			entry.Description = "authoritative catalog description"
			entry.ContentSize = 1234
			entry.ContentMD5 = "fedcba9876543210fedcba9876543210"
			entry.Tags = []string{"catalog", "exact-entry"}
			fixture := installFixture{
				snapshot:       domain.AgentSnapshot{Agents: []domain.AgentID{test.agent}},
				catalogEntries: []domain.CatalogEntry{entry},
				selected:       ref.Version,
			}
			if test.configure != nil {
				test.configure(&fixture)
			}
			var calls []string
			service := newInstallService(t, &calls, fixture)

			result, err := service.Install(context.Background(), cli.Flags{
				Agent: string(test.agent), Type: string(test.powerType), Name: ref.Name, Version: ref.Version,
			})
			if err != nil {
				t.Fatalf("Install() error = %v", err)
			}
			if len(result.Items) != 1 || result.Items[0].Power != ref {
				t.Fatalf("Install() = %#v, want one result for %+v", result, ref)
			}
			got := test.entries(service)
			if len(got) != 1 || !reflect.DeepEqual(got[0], entry) {
				t.Fatalf("content-boundary entries = %#v, want exact snapshot entry %#v once", got, entry)
			}
			if countEvent(calls, "catalog:snapshot-locked") != 1 {
				t.Fatalf("calls = %v, want SnapshotLocked exactly once", calls)
			}
			for _, forbidden := range []string{"cloud:catalog-version", "cloud:download-catalog", "cloud:unexpected-download-plugin"} {
				if containsEvent(calls, forbidden) {
					t.Fatalf("calls = %v, content path must not use legacy discovery event %q", calls, forbidden)
				}
			}
		})
	}
}

func TestInstallStaleCatalogArtifactAvailabilityMatrix(t *testing.T) {
	for _, test := range []struct {
		name       string
		seedCache  bool
		wantErr    bool
		wantSource int
	}{
		{name: "cached artifact succeeds offline", seedCache: true},
		{name: "cache miss forwards entry and fails offline", wantErr: true, wantSource: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			cacheHome := t.TempDir()
			t.Setenv("APM_HOME", home)
			t.Setenv("XDG_CACHE_HOME", cacheHome)
			ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}
			entry := catalogEntryForInstallTest(ref, "stale snapshot metadata")
			entry.Tags = []string{"offline"}
			if test.seedCache {
				marker := filepath.Join(cacheHome, "apm", "skills", ref.Name, ref.Version, "SKILL.md")
				if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
					t.Fatal(err)
				}
				contents := "---\nname: demo\nversion: \"v2.0.0\"\ndescription: cached\n---\n"
				if err := os.WriteFile(marker, []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			syncCause := errors.New("catalog origin is offline")
			contentCause := errors.New("content origin is offline")
			fixture := installFixture{
				snapshot:       domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
				catalogEntries: []domain.CatalogEntry{entry},
				catalogStatus:  catalog.SyncStatus{Stale: true, Cause: syncCause},
				selected:       ref.Version,
				cloudGetErr:    contentCause,
			}
			var calls []string
			cloudClient := &installFakeCloud{calls: &calls, fixture: &fixture}
			powerCache, err := cache.NewFilesystem(cloudClient)
			if err != nil {
				t.Fatal(err)
			}
			service, err := NewInstall(
				&installFakeLocker{calls: &calls, fixture: &fixture},
				&installFakeTransactions{calls: &calls, fixture: &fixture},
				&installFakeCatalogManager{calls: &calls, fixture: &fixture},
				cloudClient,
				powerCache,
				&installFakePluginResources{calls: &calls},
				&installFakeRegistry{calls: &calls, fixture: &fixture},
				&installFakeConfig{calls: &calls, fixture: &fixture},
				&installFakeVersions{selected: ref.Version},
			)
			if err != nil {
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			originalLogger := slog.Default()
			slog.SetDefault(logging.NewLogger(&stdout, &stderr, false))
			t.Cleanup(func() { slog.SetDefault(originalLogger) })
			result, installErr := service.Install(context.Background(), cli.Flags{
				Agent: "codex", Type: "skill", Name: ref.Name, Version: ref.Version,
			})
			if test.wantErr {
				if !errors.Is(installErr, contentCause) || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeFailed {
					t.Fatalf("Install() = %#v, %v, want failed item with %v", result, installErr, contentCause)
				}
			} else if installErr != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeInstalled {
				t.Fatalf("Install() = %#v, %v, want offline cached success", result, installErr)
			}
			if len(fixture.cloudEntries) != test.wantSource {
				t.Fatalf("content source entries = %#v, want %d call(s)", fixture.cloudEntries, test.wantSource)
			}
			if test.wantSource == 1 && !reflect.DeepEqual(fixture.cloudEntries[0], entry) {
				t.Fatalf("content source entry = %#v, want exact stale entry %#v", fixture.cloudEntries[0], entry)
			}
			if countEvent(calls, "catalog:snapshot-locked") != 1 {
				t.Fatalf("calls = %v, want SnapshotLocked exactly once", calls)
			}
			if strings.Count(stderr.String(), `msg="catalog snapshot is stale"`) != 1 || strings.Count(stderr.String(), "level=WARN") != 1 {
				t.Fatalf("stderr = %q, want exactly one stale warning", stderr.String())
			}
			if !strings.Contains(stderr.String(), "command=install") || !strings.Contains(stderr.String(), syncCause.Error()) {
				t.Fatalf("stderr = %q, want install command and stale cause", stderr.String())
			}
		})
	}
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
		"acquire", "recover", "refresh", "config:find:codex", "config:find:hermes-agent", "catalog:snapshot-locked", "release",
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
	wantCalls := []string{"acquire", "recover", "refresh", "catalog:snapshot-locked", "release"}
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
	catalogs := &installFakeCatalogManager{calls: &calls, fixture: fixture}
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
			return NewInstall(nil, transactions, catalogs, cloud, cache, pluginResources, registry, config, versions)
		}, want: "create Install lifecycle with nil locker"},
		{name: "typed nil locker", new: func() (*InstallService, error) {
			return NewInstall((*installFakeLocker)(nil), transactions, catalogs, cloud, cache, pluginResources, registry, config, versions)
		}, want: "create Install lifecycle with nil locker"},
		{name: "transaction manager", new: func() (*InstallService, error) {
			return NewInstall(locker, nil, catalogs, cloud, cache, pluginResources, registry, config, versions)
		}, want: "create Install lifecycle with nil transaction manager"},
		{name: "typed nil transaction manager", new: func() (*InstallService, error) {
			return NewInstall(locker, (*installFakeTransactions)(nil), catalogs, cloud, cache, pluginResources, registry, config, versions)
		}, want: "create Install lifecycle with nil transaction manager"},
		{name: "catalog manager", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, nil, cloud, cache, pluginResources, registry, config, versions)
		}, want: "create Install lifecycle with nil catalog manager"},
		{name: "typed nil catalog manager", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, (*installFakeCatalogManager)(nil), cloud, cache, pluginResources, registry, config, versions)
		}, want: "create Install lifecycle with nil catalog manager"},
		{name: "Cloud client", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, catalogs, nil, cache, pluginResources, registry, config, versions)
		}, want: "create Install lifecycle with nil Cloud client"},
		{name: "typed nil Cloud client", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, catalogs, (*installFakeCloud)(nil), cache, pluginResources, registry, config, versions)
		}, want: "create Install lifecycle with nil Cloud client"},
		{name: "power cache", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, catalogs, cloud, nil, pluginResources, registry, config, versions)
		}, want: "create Install lifecycle with nil power cache"},
		{name: "typed nil power cache", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, catalogs, cloud, (*installFakeCache)(nil), pluginResources, registry, config, versions)
		}, want: "create Install lifecycle with nil power cache"},
		{name: "Plugin Resource manager", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, catalogs, cloud, cache, nil, registry, config, versions)
		}, want: "create Install lifecycle with nil Plugin Resource manager"},
		{name: "typed nil Plugin Resource manager", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, catalogs, cloud, cache, (*installFakePluginResources)(nil), registry, config, versions)
		}, want: "create Install lifecycle with nil Plugin Resource manager"},
		{name: "adapter registry", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, catalogs, cloud, cache, pluginResources, nil, config, versions)
		}, want: "create Install lifecycle with nil adapter registry"},
		{name: "typed nil adapter registry", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, catalogs, cloud, cache, pluginResources, (*installFakeRegistry)(nil), config, versions)
		}, want: "create Install lifecycle with nil adapter registry"},
		{name: "config repository", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, catalogs, cloud, cache, pluginResources, registry, nil, versions)
		}, want: "create Install lifecycle with nil config repository"},
		{name: "typed nil config repository", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, catalogs, cloud, cache, pluginResources, registry, (*installFakeConfig)(nil), versions)
		}, want: "create Install lifecycle with nil config repository"},
		{name: "version selector", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, catalogs, cloud, cache, pluginResources, registry, config, nil)
		}, want: "create Install lifecycle with nil version selector"},
		{name: "typed nil version selector", new: func() (*InstallService, error) {
			return NewInstall(locker, transactions, catalogs, cloud, cache, pluginResources, registry, config, (*installFakeVersions)(nil))
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
