package domain

import "testing"

func TestSubmissionStatusConstantsAreStable(t *testing.T) {
	want := map[SubmissionStatus]string{
		SubmissionReceived:         "received",
		SubmissionValidating:       "validating",
		SubmissionValidationFailed: "validation_failed",
		SubmissionReviewPending:    "review_pending",
		SubmissionRejected:         "rejected",
		SubmissionApproved:         "approved",
		SubmissionPublishing:       "publishing",
		SubmissionPublishFailed:    "publish_failed",
		SubmissionPublished:        "published",
	}
	for status, value := range want {
		if string(status) != value {
			t.Fatalf("SubmissionStatus string = %q, want %q", status, value)
		}
	}
}

func TestSubmissionOutcomeConstantsAreStable(t *testing.T) {
	if SubmissionAccepted != "accepted" || SubmissionReplayed != "replayed" {
		t.Fatalf("outcomes = %q/%q", SubmissionAccepted, SubmissionReplayed)
	}
}

func TestSubmissionRequestPreservesFields(t *testing.T) {
	request := SubmissionRequest{
		Type:           PowerTypeSkill,
		Name:           "demo",
		Version:        "v1.0.0",
		Description:    "demo resource",
		Message:        "initial submission",
		ContentType:    "application/gzip",
		Content:        []byte("bytes"),
		ContentMD5:     "8c7dd922ad47494fc02c388e12c00eac",
		IdempotencyKey: "key",
	}
	if request.Type != PowerTypeSkill || request.Name != "demo" || request.Message != "initial submission" || len(request.Content) == 0 || request.ContentMD5 == "" {
		t.Fatal("SubmissionRequest fields must be preserved")
	}
}

func TestActiveSubmissionStatusesAreExact(t *testing.T) {
	active := []SubmissionStatus{SubmissionReceived, SubmissionValidating, SubmissionReviewPending, SubmissionApproved, SubmissionPublishing}
	for _, status := range active {
		if !IsActiveSubmissionStatus(status) {
			t.Errorf("%q is not active", status)
		}
	}
	terminal := []SubmissionStatus{SubmissionValidationFailed, SubmissionRejected, SubmissionPublishFailed, SubmissionPublished, ""}
	for _, status := range terminal {
		if IsActiveSubmissionStatus(status) {
			t.Errorf("%q is active", status)
		}
	}
}

func TestSubmissionModelsCarryMessageAndTimeline(t *testing.T) {
	detail := Submission{
		SubmissionID: "sub_1",
		Description:  "demo resource",
		Message:      "first",
		CreatedAt:    "2026-08-24T10:00:00Z",
		UpdatedAt:    "2026-08-24T10:01:00Z",
		Timeline: []SubmissionEvent{{
			EventID:    "evt_1",
			EventType:  "received",
			StatusFrom: "",
			StatusTo:   SubmissionReceived,
			Timestamp:  "2026-08-24T10:00:00Z",
			Message:    "first",
		}},
	}
	request := SubmissionRequest{Message: "first"}
	list := SubmissionList{Items: []SubmissionSummary{{SubmissionID: "sub_1"}}}
	if detail.Message != request.Message || len(detail.Timeline) != 1 || len(list.Items) != 1 {
		t.Fatal("new contracts lost data")
	}
	if detail.Timeline[0].StatusTo != SubmissionReceived || detail.Timeline[0].Message != "first" {
		t.Fatal("submission event fields must be preserved")
	}
}
