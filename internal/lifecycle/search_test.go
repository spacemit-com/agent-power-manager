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
	"apm/internal/lock"
	"apm/internal/logging"
	"apm/internal/transaction"
)

type stubUnlock struct {
	calls  *[]string
	before func()
	err    error
}

func (u *stubUnlock) Release() error {
	if u.before != nil {
		u.before()
	}
	*u.calls = append(*u.calls, "release")
	return u.err
}

type stubLocker struct {
	calls  *[]string
	unlock lock.Unlock
	err    error
}

func (l *stubLocker) Acquire() (lock.Unlock, error) {
	*l.calls = append(*l.calls, "acquire")
	return l.unlock, l.err
}

type stubTransactionManager struct {
	calls  *[]string
	result transaction.RecoveryResult
	err    error
}

func (m *stubTransactionManager) Recover(context.Context) (transaction.RecoveryResult, error) {
	*m.calls = append(*m.calls, "recover")
	return m.result, m.err
}

func (*stubTransactionManager) Install(context.Context, transaction.InstallRequest) error { return nil }

func (*stubTransactionManager) Uninstall(context.Context, transaction.UninstallRequest) error {
	return nil
}

type stubCloudClient struct {
	calls  *[]string
	before func()
	refs   []domain.PowerRef
	err    error
}

func (c *stubCloudClient) List() ([]domain.PowerRef, error) {
	if c.before != nil {
		c.before()
	}
	*c.calls = append(*c.calls, "cloud-list")
	return c.refs, c.err
}

func (*stubCloudClient) Get(domain.PowerRef) error { return nil }

func TestSearchRecoversWarnsUnlocksThenListsCloud(t *testing.T) {
	var calls []string
	var stdout, stderr bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(logging.NewLogger(&stdout, &stderr, false))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })

	unlock := &stubUnlock{
		calls: &calls,
		before: func() {
			if !strings.Contains(stderr.String(), "level=WARN") {
				t.Fatalf("stderr before lock release = %q, want recovery WARN", stderr.String())
			}
		},
	}
	cloud := &stubCloudClient{
		calls: &calls,
		refs:  []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}},
		before: func() {
			if !strings.Contains(stderr.String(), "level=WARN") {
				t.Fatalf("stderr before Cloud list = %q, want recovery WARN", stderr.String())
			}
		},
	}
	service, err := New(
		&stubLocker{calls: &calls, unlock: unlock},
		&stubTransactionManager{calls: &calls, result: transaction.RecoveryResult{
			Recovered:     true,
			Operation:     transaction.OperationInstall,
			Agent:         domain.AgentCodex,
			Power:         domain.PowerRef{Name: "demo", Version: "v2.0.0"},
			OriginalPhase: transaction.PhasePublishing,
			Direction:     transaction.RecoveryCommit,
			Actions:       []transaction.RecoveryAction{},
		}},
		cloud,
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Search(context.Background(), cli.Flags{Keyword: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"acquire", "recover", "release", "cloud-list"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if want := []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}}; !reflect.DeepEqual(result.Powers, want) {
		t.Fatalf("result.Powers = %#v, want %#v", result.Powers, want)
	}
	for _, want := range []string{
		"operation=install", "agent=codex", "power=demo",
		"version=v2.0.0", "phase=publishing",
		"direction=commit", "actions=",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want %q", stderr.String(), want)
		}
	}
}

func TestSearchNoJournalHasNoNormalOutput(t *testing.T) {
	var calls []string
	var stdout, stderr bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(logging.NewLogger(&stdout, &stderr, false))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })

	service := newSearchService(t, &calls, transaction.RecoveryResult{}, nil, nil, nil)
	if _, err := service.Search(context.Background(), cli.Flags{Keyword: "demo"}); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("output = stdout %q stderr %q, want no output", stdout.String(), stderr.String())
	}
}

func TestSearchNoJournalWritesDebugWhenVerbose(t *testing.T) {
	var calls []string
	var stdout, stderr bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(logging.NewLogger(&stdout, &stderr, true))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })

	service := newSearchService(t, &calls, transaction.RecoveryResult{}, nil, nil, nil)
	if _, err := service.Search(context.Background(), cli.Flags{Keyword: "demo"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"level=DEBUG", "msg=\"no active transaction before search\""} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want %q", stderr.String(), want)
		}
	}
}

func TestSearchAcquireFailureStopsBeforeRecovery(t *testing.T) {
	var calls []string
	wantErr := errors.New("lock unavailable")
	service, err := New(
		&stubLocker{calls: &calls, err: wantErr},
		&stubTransactionManager{calls: &calls},
		&stubCloudClient{calls: &calls},
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Search(context.Background(), cli.Flags{Keyword: "demo"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Search() error = %v, want wrapped %v", err, wantErr)
	}
	if want := []string{"acquire"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestSearchRecoveryFailureReleasesAndStopsBeforeCloud(t *testing.T) {
	var calls []string
	wantErr := errors.New("recovery failed")
	service := newSearchService(t, &calls, transaction.RecoveryResult{}, wantErr, nil, nil)

	_, err := service.Search(context.Background(), cli.Flags{Keyword: "demo"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Search() error = %v, want wrapped %v", err, wantErr)
	}
	if want := []string{"acquire", "recover", "release"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestSearchJoinsRecoveryAndReleaseFailures(t *testing.T) {
	var calls []string
	recoveryErr := errors.New("recovery failed")
	releaseErr := errors.New("release failed")
	service := newSearchService(t, &calls, transaction.RecoveryResult{}, recoveryErr, releaseErr, nil)

	_, err := service.Search(context.Background(), cli.Flags{Keyword: "demo"})
	if !errors.Is(err, recoveryErr) || !errors.Is(err, releaseErr) {
		t.Fatalf("Search() error = %v, want joined recovery and release errors", err)
	}
	if want := []string{"acquire", "recover", "release"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestSearchReleaseFailureStopsBeforeCloud(t *testing.T) {
	var calls []string
	wantErr := errors.New("release failed")
	service := newSearchService(t, &calls, transaction.RecoveryResult{}, nil, wantErr, nil)

	_, err := service.Search(context.Background(), cli.Flags{Keyword: "demo"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Search() error = %v, want wrapped %v", err, wantErr)
	}
	if want := []string{"acquire", "recover", "release"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestSearchCloudFailurePreservesCause(t *testing.T) {
	var calls []string
	wantErr := errors.New("Cloud unavailable")
	service := newSearchService(t, &calls, transaction.RecoveryResult{}, nil, nil, wantErr)

	_, err := service.Search(context.Background(), cli.Flags{Keyword: "demo"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Search() error = %v, want wrapped %v", err, wantErr)
	}
	if want := []string{"acquire", "recover", "release", "cloud-list"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestNewRejectsNilSearchDependencies(t *testing.T) {
	var calls []string
	locker := &stubLocker{calls: &calls, unlock: &stubUnlock{calls: &calls}}
	transactions := &stubTransactionManager{calls: &calls}
	cloud := &stubCloudClient{calls: &calls}

	tests := []struct {
		name string
		new  func() (*Service, error)
		want string
	}{
		{name: "locker", new: func() (*Service, error) { return New(nil, transactions, cloud) }, want: "create lifecycle service with nil locker"},
		{name: "transaction manager", new: func() (*Service, error) { return New(locker, nil, cloud) }, want: "create lifecycle service with nil transaction manager"},
		{name: "Cloud client", new: func() (*Service, error) { return New(locker, transactions, nil) }, want: "create lifecycle service with nil cloud client"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := tt.new()
			if service != nil || err == nil || err.Error() != tt.want {
				t.Fatalf("New() = (%v, %v), want (nil, %q)", service, err, tt.want)
			}
		})
	}
}

func newSearchService(t *testing.T, calls *[]string, result transaction.RecoveryResult, recoveryErr, releaseErr, cloudErr error) *Service {
	t.Helper()
	service, err := New(
		&stubLocker{calls: calls, unlock: &stubUnlock{calls: calls, err: releaseErr}},
		&stubTransactionManager{calls: calls, result: result, err: recoveryErr},
		&stubCloudClient{calls: calls, err: cloudErr},
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
