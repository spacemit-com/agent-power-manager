//go:build linux

package local

import (
	"errors"
	"io/fs"
	"testing"
	"time"

	"apm/internal/cloud"
)

func TestRejectMultipleLinksFailsClosedWithoutLinuxLinkMetadata(t *testing.T) {
	err := rejectMultipleLinks(fileInfoWithoutSystemMetadata{}, "payload.md")
	if !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("rejectMultipleLinks() error = %v, want ErrUnsafeContent", err)
	}
}

type fileInfoWithoutSystemMetadata struct{}

func (fileInfoWithoutSystemMetadata) Name() string       { return "payload.md" }
func (fileInfoWithoutSystemMetadata) Size() int64        { return 1 }
func (fileInfoWithoutSystemMetadata) Mode() fs.FileMode  { return 0o644 }
func (fileInfoWithoutSystemMetadata) ModTime() time.Time { return time.Time{} }
func (fileInfoWithoutSystemMetadata) IsDir() bool        { return false }
func (fileInfoWithoutSystemMetadata) Sys() any           { return nil }
