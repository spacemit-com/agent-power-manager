package cloud

import (
	"context"
	"errors"
	"testing"

	"apm/internal/domain"
)

type stubSubmission struct{ err error }

func (s stubSubmission) Submit(context.Context, domain.SubmissionRequest) (domain.SubmissionReceipt, error) {
	return domain.SubmissionReceipt{}, s.err
}
func (s stubSubmission) GetSubmission(context.Context, string) (domain.Submission, error) {
	return domain.Submission{}, s.err
}
func (s stubSubmission) ListSubmissions(context.Context, domain.SubmissionListQuery) (domain.SubmissionList, error) {
	return domain.SubmissionList{}, s.err
}

var _ Submission = stubSubmission{}

func TestSubmissionErrorsAreDistinct(t *testing.T) {
	set := map[error]bool{
		ErrSubmissionUnsupported: true,
		ErrSubmissionConflict:    true,
		ErrAuth:                  true,
		ErrUnavailable:           true,
	}
	if len(set) != 4 {
		t.Fatal("submission errors must be distinct sentinel values")
	}
}

func TestSubmissionStubPropagatesError(t *testing.T) {
	stub := stubSubmission{err: ErrSubmissionUnsupported}
	if _, err := stub.Submit(context.Background(), domain.SubmissionRequest{}); !errors.Is(err, ErrSubmissionUnsupported) {
		t.Fatalf("err = %v, want ErrSubmissionUnsupported", err)
	}
}
