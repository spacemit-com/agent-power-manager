package cloud_test

import (
	"errors"
	"fmt"
	"testing"

	"apm/internal/cloud"
)

func TestCloudErrorSentinelsClassifyWrappedErrors(t *testing.T) {
	for _, want := range []error{
		cloud.ErrNotFound,
		cloud.ErrYanked,
		cloud.ErrInvalidResponse,
		cloud.ErrIntegrity,
		cloud.ErrUnsafeContent,
		cloud.ErrProviderConfig,
	} {
		if err := fmt.Errorf("provider request: %w", want); !errors.Is(err, want) {
			t.Fatalf("errors.Is(%v, %v) = false", err, want)
		}
	}
}

func TestTooManyActiveSubmissionsCanJoinSubmissionConflict(t *testing.T) {
	err := errors.Join(cloud.ErrTooManyActiveSubmissions, cloud.ErrSubmissionConflict)
	if !errors.Is(err, cloud.ErrTooManyActiveSubmissions) || !errors.Is(err, cloud.ErrSubmissionConflict) {
		t.Fatalf("joined error = %v", err)
	}
	if errors.Is(err, cloud.ErrAlreadyPublished) {
		t.Fatalf("joined error = %v, must not match ErrAlreadyPublished", err)
	}
}
