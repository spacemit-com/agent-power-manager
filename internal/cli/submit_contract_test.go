package cli

import (
	"bytes"
	"testing"
)

func TestRenderSubmitIncludesOutcome(t *testing.T) {
	var out bytes.Buffer
	err := renderSubmit(&out, SubmitResult{Outcome: "replayed", SubmissionID: "sub_1", Status: "validating"})
	if err != nil {
		t.Fatal(err)
	}
	want := "outcome: replayed\nsubmission_id: sub_1\nstatus: validating\ntrack: apm submission get sub_1\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}
