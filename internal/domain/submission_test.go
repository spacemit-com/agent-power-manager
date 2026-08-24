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
		ContentType:    "application/gzip",
		Content:        []byte("bytes"),
		ContentMD5:     "8c7dd922ad47494fc02c388e12c00eac",
		IdempotencyKey: "key",
	}
	if request.Type != PowerTypeSkill || request.Name != "demo" || len(request.Content) == 0 || request.ContentMD5 == "" {
		t.Fatal("SubmissionRequest fields must be preserved")
	}
}
