package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
)

func TestNewSubmissionQueryRejectsNilCloud(t *testing.T) {
	if _, err := NewSubmissionQuery(nil); err == nil {
		t.Fatal("NewSubmissionQuery(nil) error = nil, want error")
	}
	if _, err := NewSubmissionQuery((*recordingSubmissionClient)(nil)); err == nil {
		t.Fatal("NewSubmissionQuery(typed nil) error = nil, want error")
	}
}

func TestSubmissionQueryGet(t *testing.T) {
	client := &recordingSubmissionClient{
		detail: domain.Submission{SubmissionID: "sub_1", Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0", Status: domain.SubmissionReviewPending},
	}
	service, err := NewSubmissionQuery(client)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Get(context.Background(), "sub_1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if result.Detail == nil || result.Detail.SubmissionID != "sub_1" {
		t.Fatalf("Get() = %#v", result)
	}
	if len(client.getCalls) != 1 || client.getCalls[0] != "sub_1" {
		t.Fatalf("GetSubmission calls = %v, want [sub_1]", client.getCalls)
	}
}

func TestSubmissionQueryListMapsFilters(t *testing.T) {
	client := &recordingSubmissionClient{
		list: domain.SubmissionList{Items: []domain.SubmissionSummary{{SubmissionID: "sub_1", Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0", Status: domain.SubmissionReceived}}},
	}
	service, err := NewSubmissionQuery(client)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.List(context.Background(), cli.Flags{Type: "skill", Status: "review_pending", Name: "demo"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(result.Summaries) != 1 || result.Summaries[0].SubmissionID != "sub_1" {
		t.Fatalf("List() = %#v", result)
	}
	if len(client.listCalls) != 1 {
		t.Fatalf("ListSubmissions calls = %d, want 1", len(client.listCalls))
	}
	query := client.listCalls[0]
	wantQuery := domain.SubmissionListQuery{
		Status: domain.SubmissionReviewPending,
		Type:   domain.PowerTypeSkill,
		Name:   "demo",
	}
	if !reflect.DeepEqual(query, wantQuery) {
		t.Fatalf("ListSubmissions query = %#v, want %#v", query, wantQuery)
	}
}

func TestSubmissionQueryForwardsErrors(t *testing.T) {
	queryErr := errors.New("query failed")
	client := &recordingSubmissionClient{getErr: queryErr, listErr: queryErr}
	service, err := NewSubmissionQuery(client)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Get(context.Background(), "sub_1"); !errors.Is(err, queryErr) {
		t.Fatalf("Get() error = %v, want %v", err, queryErr)
	}
	if _, err := service.List(context.Background(), cli.Flags{}); !errors.Is(err, queryErr) {
		t.Fatalf("List() error = %v, want %v", err, queryErr)
	}
}
