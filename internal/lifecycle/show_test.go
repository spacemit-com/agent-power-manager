package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"apm/internal/catalog"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/logging"
	"apm/internal/transaction"
)

func TestShowLatestAndExactVersion(t *testing.T) {
	refs := []domain.PowerRef{
		{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"},
		{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.0.0"},
	}
	var calls []string
	service := newShowService(t, &calls, refs, catalog.SyncStatus{})
	got, err := service.Show(context.Background(), cli.Flags{Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Power.Power.Version != "v2.0.0" {
		t.Fatalf("latest = %#v", got.Power.Power)
	}
	got, err = service.Show(context.Background(), cli.Flags{Name: "demo", Type: "skill", Version: "v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Power.Power.Version != "v1.0.0" {
		t.Fatalf("exact = %#v", got.Power.Power)
	}
	if !reflect.DeepEqual(calls, []string{"inspect", "catalog-snapshot", "inspect", "catalog-snapshot"}) {
		t.Fatalf("calls=%v", calls)
	}
}

func TestShowErrorsAndValidation(t *testing.T) {
	refs := []domain.PowerRef{
		{Type: domain.PowerTypeSkill, Name: "shared", Version: "v1.0.0"},
		{Type: domain.PowerTypePlugin, Name: "shared", Version: "v1.0.0"},
	}
	service := newShowService(t, nil, refs, catalog.SyncStatus{})
	for _, tc := range []struct {
		flags cli.Flags
		cause error
	}{
		{cli.Flags{Name: "missing"}, catalog.ErrNotFound},
		{cli.Flags{Name: "shared"}, catalog.ErrAmbiguous},
		{cli.Flags{Name: "shared", Type: "skill", Version: "v9.0.0"}, catalog.ErrNotFound},
	} {
		_, err := service.Show(context.Background(), tc.flags)
		if !errors.Is(err, tc.cause) {
			t.Errorf("flags=%#v err=%v, want %v", tc.flags, err, tc.cause)
		}
	}
	if _, err := service.Show(context.Background(), cli.Flags{}); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("empty name err=%v", err)
	}
	_, err := service.Show(context.Background(), cli.Flags{Name: "shared", Version: "1.0.0"})
	if err == nil || !strings.Contains(err.Error(), "invalid power version") {
		t.Fatalf("invalid version err=%v", err)
	}
}

func TestShowInspectsAndWarnsStale(t *testing.T) {
	var calls []string
	var stdout, stderr bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(logging.NewLogger(&stdout, &stderr, false))
	t.Cleanup(func() { slog.SetDefault(previous) })
	cause := errors.New("catalog unavailable")
	service := newShowService(t, &calls, []domain.PowerRef{{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}}, catalog.SyncStatus{Stale: true, Cause: cause})
	result, err := service.Show(context.Background(), cli.Flags{Name: "demo"})
	if err != nil || result.Power.Power.Name != "demo" {
		t.Fatalf("result=(%#v,%v)", result, err)
	}
	if !reflect.DeepEqual(calls, []string{"inspect", "catalog-snapshot"}) {
		t.Fatalf("calls=%v", calls)
	}
	if !strings.Contains(stderr.String(), "catalog snapshot is stale") || stdout.Len() != 0 {
		t.Fatalf("logs stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func newShowService(t *testing.T, calls *[]string, refs []domain.PowerRef, status catalog.SyncStatus) *ShowService {
	t.Helper()
	manager := &stubCatalogManager{calls: calls, snapshot: catalogSnapshotFromRefs(t, refs), status: status}
	service, err := NewShow(&fakeInspector{calls: calls}, manager)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

var _ transaction.Inspector = (*fakeInspector)(nil)
