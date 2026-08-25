package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
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

type stubCatalogManager struct {
	calls    *[]string
	snapshot catalog.Snapshot
	status   catalog.SyncStatus
	err      error
}

// stubCloudClient 保留提交生命周期测试复用的最小 Cloud 空实现。
type stubCloudClient struct{}

func (*stubCloudClient) List(context.Context) ([]domain.PowerRef, error) { return nil, nil }
func (*stubCloudClient) Resolve(context.Context, domain.PowerRef) (domain.PowerRef, error) {
	return domain.PowerRef{}, nil
}
func (*stubCloudClient) DownloadSkill(context.Context, domain.PowerRef, string) error  { return nil }
func (*stubCloudClient) DownloadPlugin(context.Context, domain.PowerRef, string) error { return nil }
func (*stubCloudClient) GetMCP(context.Context, domain.PowerRef) (domain.MCPServer, error) {
	return domain.MCPServer{}, nil
}
func (*stubCloudClient) DownloadAgent(context.Context, domain.PowerRef, string) error { return nil }
func (*stubCloudClient) Submit(context.Context, domain.SubmissionRequest) (domain.SubmissionResult, error) {
	return domain.SubmissionResult{}, nil
}
func (*stubCloudClient) GetSubmission(context.Context, string) (domain.Submission, error) {
	return domain.Submission{}, nil
}
func (*stubCloudClient) ListSubmissions(context.Context, domain.SubmissionListQuery) (domain.SubmissionList, error) {
	return domain.SubmissionList{}, nil
}

func newReadOnlyCatalogManager(t *testing.T) CatalogManager {
	t.Helper()
	return &stubCatalogManager{snapshot: catalogSnapshotFromRefs(t, nil)}
}

func (c *stubCatalogManager) Snapshot(ctx context.Context) (catalog.Snapshot, catalog.SyncStatus, error) {
	if c.calls != nil {
		*c.calls = append(*c.calls, "catalog-snapshot")
	}
	if err := ctx.Err(); err != nil {
		return catalog.Snapshot{}, catalog.SyncStatus{}, err
	}
	return c.snapshot, c.status, c.err
}

func (c *stubCatalogManager) SnapshotLocked(context.Context) (catalog.Snapshot, catalog.SyncStatus, error) {
	if c.calls != nil {
		*c.calls = append(*c.calls, "catalog-snapshot-locked")
	}
	return c.snapshot, c.status, c.err
}

func TestSearchPassesCancelledContextToCatalogManager(t *testing.T) {
	var calls []string
	service, err := NewSearch(&fakeInspector{calls: &calls}, &stubCatalogManager{calls: &calls})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = service.Search(ctx, cli.Flags{Keyword: "demo"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Search() error = %v, want context.Canceled", err)
	}
}

func TestSearchInspectsBeforeTakingOneCatalogSnapshot(t *testing.T) {
	var calls []string
	refs := []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}}
	service, err := NewSearch(
		&fakeInspector{calls: &calls, result: transaction.InspectionResult{
			Active: true, Operation: transaction.OperationInstall, Agent: domain.AgentCodex,
			Power:         domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"},
			OriginalPhase: transaction.PhasePublishing,
		}},
		&stubCatalogManager{calls: &calls, snapshot: catalogSnapshotFromRefs(t, refs)},
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Search(context.Background(), cli.Flags{Keyword: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"inspect", "catalog-snapshot"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if want := []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}}; !reflect.DeepEqual(result.Powers, want) {
		t.Fatalf("result.Powers = %#v, want %#v", result.Powers, want)
	}
}

func TestSearchDamagedJournalContinuesToCatalog(t *testing.T) {
	var calls []string
	managerErr := errors.New("catalog unavailable")
	service, err := NewSearch(
		&fakeInspector{calls: &calls, err: errors.New("journal checksum failure")},
		&stubCatalogManager{calls: &calls, err: managerErr},
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Search(context.Background(), cli.Flags{Keyword: "demo"})
	if !errors.Is(err, managerErr) {
		t.Fatalf("Search() error = %v, want wrapped %v", err, managerErr)
	}
	if len(result.Powers) != 0 {
		t.Fatalf("Search() result = %#v, want zero result when Cloud List fails", result)
	}
	if want := []string{"inspect", "catalog-snapshot"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestNewSearchRejectsNilDependencies(t *testing.T) {
	var calls []string
	inspector := &fakeInspector{calls: &calls}
	manager := &stubCatalogManager{calls: &calls}

	tests := []struct {
		name string
		new  func() (*SearchService, error)
		want string
	}{
		{name: "inspector", new: func() (*SearchService, error) { return NewSearch(nil, manager) }, want: "create Search lifecycle with nil transaction inspector"},
		{name: "typed nil inspector", new: func() (*SearchService, error) { return NewSearch((*fakeInspector)(nil), manager) }, want: "create Search lifecycle with nil transaction inspector"},
		{name: "catalog manager", new: func() (*SearchService, error) { return NewSearch(inspector, nil) }, want: "create Search lifecycle with nil catalog manager"},
		{name: "typed nil catalog manager", new: func() (*SearchService, error) { return NewSearch(inspector, (*stubCatalogManager)(nil)) }, want: "create Search lifecycle with nil catalog manager"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := tt.new()
			if service != nil || err == nil || err.Error() != tt.want {
				t.Fatalf("NewSearch() = (%v, %v), want (nil, %q)", service, err, tt.want)
			}
		})
	}
}

func TestSearchCatalogStaleWarningIsRenderedOnceOutsideResult(t *testing.T) {
	var calls []string
	var stdout, stderr bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(logging.NewLogger(&stdout, &stderr, false))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })
	cause := errors.New("catalog source unavailable")
	refs := []domain.PowerRef{{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}}
	service, err := NewSearch(
		&fakeInspector{calls: &calls},
		&stubCatalogManager{calls: &calls, snapshot: catalogSnapshotFromRefs(t, refs), status: catalog.SyncStatus{Stale: true, Cause: cause}},
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Search(context.Background(), cli.Flags{Keyword: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"inspect", "catalog-snapshot"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if got := strings.Count(stderr.String(), "level=WARN"); got != 1 || !strings.Contains(stderr.String(), "catalog snapshot is stale") || !strings.Contains(stderr.String(), cause.Error()) || stdout.Len() != 0 {
		t.Fatalf("output = stdout %q stderr %q, want one stale warning", stdout.String(), stderr.String())
	}
	data, marshalErr := json.Marshal(result)
	if marshalErr != nil || strings.Contains(string(data), "stale") || strings.Contains(string(data), cause.Error()) {
		t.Fatalf("JSON result = %q, %v, want no warning data", data, marshalErr)
	}
}
