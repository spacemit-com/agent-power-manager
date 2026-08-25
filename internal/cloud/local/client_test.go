package local_test

import (
	"testing"

	"apm/internal/cloud"
	"apm/internal/cloud/local"
)

func TestNewRejectsRelativeResourceRoot(t *testing.T) {
	if _, err := local.New("resources"); err == nil {
		t.Fatal("New() error = nil, want relative-path error")
	}
}

func TestLocalClientImplementsFinalCloudContract(t *testing.T) {
	client, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var provider cloud.Client = client
	if provider == nil {
		t.Fatal("Local cloud.Client is nil")
	}
}
