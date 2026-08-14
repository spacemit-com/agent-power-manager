package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"apm/internal/domain"
	"apm/internal/lock"
	"apm/internal/logging"
	"apm/internal/transaction"
)

func TestNewFixRejectsNilDependencies(t *testing.T) {
	var calls []string
	locker := &fixFakeLocker{calls: &calls}
	recoverer := &fixFakeRecoverer{calls: &calls}

	tests := []struct {
		name string
		new  func() (*FixService, error)
		want string
	}{
		{name: "locker", new: func() (*FixService, error) { return NewFix(nil, recoverer) }, want: "create Fix lifecycle with nil locker"},
		{name: "typed nil locker", new: func() (*FixService, error) {
			return NewFix((*fixFakeLocker)(nil), recoverer)
		}, want: "create Fix lifecycle with nil locker"},
		{name: "recoverer", new: func() (*FixService, error) { return NewFix(locker, nil) }, want: "create Fix lifecycle with nil transaction recoverer"},
		{name: "typed nil recoverer", new: func() (*FixService, error) {
			return NewFix(locker, (*fixFakeRecoverer)(nil))
		}, want: "create Fix lifecycle with nil transaction recoverer"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := tt.new()
			if service != nil || err == nil || err.Error() != tt.want {
				t.Fatalf("NewFix() = (%v, %v), want (nil, %q)", service, err, tt.want)
			}
		})
	}
}

func TestFixRecoversOnceLogsBeforeReleaseAndDoesNotWriteStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	fixTestLogger(t, &stdout, &stderr, false)

	var calls []string
	locker := &fixFakeLocker{calls: &calls, beforeRelease: func() {
		assertContainsAll(t, stderr.String(), []string{
			"level=WARN", `msg="transaction recovered"`, "operation=install",
			"agent=codex", "power=demo", "version=v1.0.0",
			"phase=publishing", "direction=commit", "actions=",
		})
	}}
	recoverer := &fixFakeRecoverer{calls: &calls, result: recoveredFixResult()}
	service, err := NewFix(locker, recoverer)
	if err != nil {
		t.Fatal(err)
	}

	if err := service.Fix(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"acquire", "recover", "release"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if recoverer.count != 1 {
		t.Fatalf("Recover calls = %d, want 1", recoverer.count)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if strings.Count(stderr.String(), "level=WARN") != 1 {
		t.Fatalf("stderr = %q, want exactly one recovery Warn", stderr.String())
	}
}

func TestFixNoJournalLogsDebugOnlyWhenVerbose(t *testing.T) {
	var stdout, stderr bytes.Buffer
	fixTestLogger(t, &stdout, &stderr, true)

	var calls []string
	service, err := NewFix(&fixFakeLocker{calls: &calls}, &fixFakeRecoverer{calls: &calls})
	if err != nil {
		t.Fatal(err)
	}

	if err := service.Fix(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"acquire", "recover", "release"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if got := stderr.String(); !strings.Contains(got, `level=DEBUG msg="no active transaction before fix"`) || strings.Count(got, "\n") != 1 {
		t.Fatalf("stderr = %q, want exactly one Debug diagnostic", got)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestFixErrorMatrix(t *testing.T) {
	acquireCause := errors.New("acquire failed")
	recoverCause := errors.New("recover failed")
	releaseCause := errors.New("release failed")

	tests := []struct {
		name       string
		acquireErr error
		recoverErr error
		releaseErr error
		result     transaction.RecoveryResult
		wantCalls  []string
		causes     []error
		wantWarn   bool
	}{
		{name: "acquire fails", acquireErr: acquireCause, wantCalls: []string{"acquire"}, causes: []error{acquireCause}},
		{name: "recover fails", recoverErr: recoverCause, wantCalls: []string{"acquire", "recover", "release"}, causes: []error{recoverCause}},
		{name: "release fails after no journal", releaseErr: releaseCause, wantCalls: []string{"acquire", "recover", "release"}, causes: []error{releaseCause}},
		{name: "recover and release fail", recoverErr: recoverCause, releaseErr: releaseCause, wantCalls: []string{"acquire", "recover", "release"}, causes: []error{recoverCause, releaseCause}},
		{name: "recovery succeeds and release fails", releaseErr: releaseCause, result: recoveredFixResult(), wantCalls: []string{"acquire", "recover", "release"}, causes: []error{releaseCause}, wantWarn: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			fixTestLogger(t, &stdout, &stderr, false)
			var calls []string
			locker := &fixFakeLocker{calls: &calls, acquireErr: tt.acquireErr, releaseErr: tt.releaseErr}
			recoverer := &fixFakeRecoverer{calls: &calls, result: tt.result, err: tt.recoverErr}
			service, err := NewFix(locker, recoverer)
			if err != nil {
				t.Fatal(err)
			}

			err = service.Fix(context.Background())
			if err == nil {
				t.Fatal("Fix() error = nil, want error")
			}
			for _, cause := range tt.causes {
				if !errors.Is(err, cause) {
					t.Fatalf("Fix() error = %v, want cause %v", err, cause)
				}
			}
			if !reflect.DeepEqual(calls, tt.wantCalls) {
				t.Fatalf("calls = %v, want %v", calls, tt.wantCalls)
			}
			if got, want := recoverer.count, boolToCount(tt.acquireErr == nil); got != want {
				t.Fatalf("Recover calls = %d, want %d", got, want)
			}
			if tt.wantWarn && !bytes.Contains(stderr.Bytes(), []byte(`msg="transaction recovered"`)) {
				t.Fatalf("stderr = %q, want recovery Warn despite release failure", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func recoveredFixResult() transaction.RecoveryResult {
	return transaction.RecoveryResult{
		Recovered:     true,
		Operation:     transaction.OperationInstall,
		Agent:         domain.AgentCodex,
		Power:         domain.PowerRef{Name: "demo", Version: "v1.0.0"},
		OriginalPhase: transaction.PhasePublishing,
		Direction:     transaction.RecoveryCommit,
		Actions:       []transaction.RecoveryAction{{Kind: transaction.RecoveryActionSetManifest}},
	}
}

func boolToCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func fixTestLogger(t *testing.T, stdout, stderr *bytes.Buffer, verbose bool) {
	t.Helper()
	originalLogger := slog.Default()
	slog.SetDefault(logging.NewLogger(stdout, stderr, verbose))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })
}

type fixFakeLocker struct {
	calls         *[]string
	acquireErr    error
	releaseErr    error
	beforeRelease func()
}

func (f *fixFakeLocker) Acquire() (lock.Unlock, error) {
	*f.calls = append(*f.calls, "acquire")
	if f.acquireErr != nil {
		return nil, f.acquireErr
	}
	return &fixFakeUnlock{locker: f}, nil
}

type fixFakeUnlock struct{ locker *fixFakeLocker }

func (f *fixFakeUnlock) Release() error {
	if f.locker.beforeRelease != nil {
		f.locker.beforeRelease()
	}
	*f.locker.calls = append(*f.locker.calls, "release")
	return f.locker.releaseErr
}

type fixFakeRecoverer struct {
	calls  *[]string
	result transaction.RecoveryResult
	err    error
	count  int
}

func (f *fixFakeRecoverer) Recover(context.Context) (transaction.RecoveryResult, error) {
	*f.calls = append(*f.calls, "recover")
	f.count++
	return f.result, f.err
}
