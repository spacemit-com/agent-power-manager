//go:build linux

package local

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

func TestDownloadSkillRejectsSourceRootSwapBeforeDescriptorSnapshot(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "skill")
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	client.beforeTreeSnapshot = func() {
		if err := os.Rename(source, source+"-original"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(t.TempDir(), source); err != nil {
			t.Fatal(err)
		}
	}
	destination := filepath.Join(t.TempDir(), "download")

	err = client.DownloadSkill(context.Background(), testSkillRef, destination)

	assertUnsafeNoDownloadResidue(t, err, destination)
}

func TestDownloadSkillRejectsFileGrowthBetweenMetadataAndDescriptorRead(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "skill")
	payload := filepath.Join(source, "payload.md")
	writeTestFile(t, payload, "safe")
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	client.beforeSnapshotFileOpen = func(relative string) {
		if relative == "payload.md" {
			if err := os.Truncate(payload, maxTreeFileSize+1); err != nil {
				t.Fatal(err)
			}
		}
	}
	destination := filepath.Join(t.TempDir(), "download")

	err = client.DownloadSkill(context.Background(), testSkillRef, destination)

	assertUnsafeNoDownloadResidue(t, err, destination)
}

func TestDownloadSkillRejectsFileSwapBetweenMetadataAndDescriptorRead(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "skill")
	payload := filepath.Join(source, "payload.md")
	writeTestFile(t, payload, "validated")
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	client.beforeSnapshotFileOpen = func(relative string) {
		if relative == "payload.md" {
			if err := os.Rename(payload, payload+".original"); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, payload, "replacement")
		}
	}
	destination := filepath.Join(t.TempDir(), "download")

	err = client.DownloadSkill(context.Background(), testSkillRef, destination)

	assertUnsafeNoDownloadResidue(t, err, destination)
}

func TestDownloadSkillPublishesValidatedSnapshotWhenSourceChangesAfterRead(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "skill")
	payload := filepath.Join(source, "payload.md")
	writeTestFile(t, payload, "validated")
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	client.beforeTreePublish = func() {
		writeTestBytes(t, payload, []byte{'b', 0, 'd'})
	}
	destination := filepath.Join(t.TempDir(), "download")

	if err := client.DownloadSkill(context.Background(), testSkillRef, destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "payload.md"))
	if err != nil || string(got) != "validated" {
		t.Fatalf("published payload = %q, %v, want validated snapshot", got, err)
	}
}

func TestDownloadSkillCancellationBeforePublishLeavesNoDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "skill")
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	client.beforeTreePublish = cancel
	destination := filepath.Join(t.TempDir(), "download")

	err = client.DownloadSkill(ctx, testSkillRef, destination)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DownloadSkill() error = %v, want context.Canceled", err)
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination stat error = %v, want absent", statErr)
	}
	assertNoSiblingTemps(t, destination)
}

func TestWriteAllContextStopsWhenCanceledDuringCopy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var destination bytes.Buffer
	writtenChunks := 0
	writer := writerFunc(func(contents []byte) (int, error) {
		writtenChunks++
		count, err := destination.Write(contents)
		cancel()
		return count, err
	})
	payload := bytes.Repeat([]byte("x"), 128<<10)

	err := writeAllContext(ctx, writer, payload)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("writeAllContext() error = %v, want context.Canceled", err)
	}
	if writtenChunks != 1 || destination.Len() >= len(payload) {
		t.Fatalf("copy wrote %d chunks/%d bytes, want one partial chunk", writtenChunks, destination.Len())
	}
}

func TestGetMCPCancellationAfterDecodeReturnsCanceled(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "mcps", "demo", "v1.0.0", "MCP.json")
	writeTestFile(t, marker, `{"transport":"stdio","command":"npx"}`)
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	client.afterMCPDecode = cancel

	_, err = client.GetMCP(ctx, domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.0.0"})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetMCP() error = %v, want context.Canceled", err)
	}
}

type writerFunc func([]byte) (int, error)

func (write writerFunc) Write(contents []byte) (int, error) {
	return write(contents)
}
