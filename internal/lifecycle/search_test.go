package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"
)

type stubCloudClient struct {
	calls *[]string
	refs  []domain.PowerRef
	err   error
}

func (c *stubCloudClient) List(ctx context.Context) ([]domain.PowerRef, error) {
	*c.calls = append(*c.calls, "cloud-list")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.refs, c.err
}

func (c *stubCloudClient) Resolve(ctx context.Context, ref domain.PowerRef) (domain.PowerRef, error) {
	if err := ctx.Err(); err != nil {
		return domain.PowerRef{}, err
	}
	return ref, nil
}

func (*stubCloudClient) DownloadSkill(context.Context, domain.PowerRef, string) error { return nil }
func (*stubCloudClient) DownloadAgent(context.Context, domain.PowerRef, string) error { return nil }
func (*stubCloudClient) GetMCP(context.Context, domain.PowerRef) (domain.MCPServer, error) {
	return domain.MCPServer{}, nil
}

func TestSearchPassesCancelledContextToCloud(t *testing.T) {
	var calls []string
	service, err := NewSearch(&fakeInspector{calls: &calls}, &stubCloudClient{calls: &calls})
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
func (*stubCloudClient) DownloadPlugin(context.Context, domain.PowerRef, string) error {
	return errors.New("unexpected Plugin download")
}

func TestSearchInspectsBeforeListingCloud(t *testing.T) {
	var calls []string
	service, err := NewSearch(
		&fakeInspector{calls: &calls, result: transaction.InspectionResult{
			Active: true, Operation: transaction.OperationInstall, Agent: domain.AgentCodex,
			Power:         domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"},
			OriginalPhase: transaction.PhasePublishing,
		}},
		&stubCloudClient{calls: &calls, refs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}}},
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Search(context.Background(), cli.Flags{Keyword: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"inspect", "cloud-list"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if want := []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}}; !reflect.DeepEqual(result.Powers, want) {
		t.Fatalf("result.Powers = %#v, want %#v", result.Powers, want)
	}
}

func TestSearchDamagedJournalContinuesToCloud(t *testing.T) {
	var calls []string
	cloudErr := errors.New("Cloud unavailable")
	service, err := NewSearch(
		&fakeInspector{calls: &calls, err: errors.New("journal checksum failure")},
		&stubCloudClient{calls: &calls, err: cloudErr},
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Search(context.Background(), cli.Flags{Keyword: "demo"})
	if !errors.Is(err, cloudErr) {
		t.Fatalf("Search() error = %v, want wrapped %v", err, cloudErr)
	}
	if len(result.Powers) != 0 {
		t.Fatalf("Search() result = %#v, want zero result when Cloud List fails", result)
	}
	if want := []string{"inspect", "cloud-list"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestNewSearchRejectsNilDependencies(t *testing.T) {
	var calls []string
	inspector := &fakeInspector{calls: &calls}
	cloud := &stubCloudClient{calls: &calls}

	tests := []struct {
		name string
		new  func() (*SearchService, error)
		want string
	}{
		{name: "inspector", new: func() (*SearchService, error) { return NewSearch(nil, cloud) }, want: "create Search lifecycle with nil transaction inspector"},
		{name: "typed nil inspector", new: func() (*SearchService, error) { return NewSearch((*fakeInspector)(nil), cloud) }, want: "create Search lifecycle with nil transaction inspector"},
		{name: "Cloud client", new: func() (*SearchService, error) { return NewSearch(inspector, nil) }, want: "create Search lifecycle with nil Cloud client"},
		{name: "typed nil Cloud client", new: func() (*SearchService, error) { return NewSearch(inspector, (*stubCloudClient)(nil)) }, want: "create Search lifecycle with nil Cloud client"},
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
