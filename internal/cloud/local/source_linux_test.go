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
	"golang.org/x/sys/unix"
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

func TestOpenatSnapshotBackendDownloadsValidSkillWithoutOpenat2(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "skill")
	writeTestFile(t, filepath.Join(source, "payload.md"), "payload")

	snapshot, err := capturePhysicalTreeOpenat(context.Background(), root, source, nil, nil, nil)
	if err != nil {
		t.Fatalf("capturePhysicalTreeOpenat() error = %v", err)
	}
	if err := snapshot.requireRegularFile("SKILL.md"); err != nil {
		t.Fatal(err)
	}
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

func TestDownloadSkillRejectsSpecialNodeSwapBeforeAnyDataOpen(t *testing.T) {
	for _, replacement := range []string{"fifo", "device"} {
		t.Run(replacement, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
			writeTestFile(t, filepath.Join(source, "SKILL.md"), "skill")
			payload := filepath.Join(source, "payload.md")
			writeTestFile(t, payload, "validated")
			client, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			dataOpened := false
			client.afterSnapshotDataOpen = func(relative string) {
				if relative == "payload.md" {
					dataOpened = true
				}
			}
			client.beforeSnapshotFileOpen = func(relative string) {
				if relative != "payload.md" {
					return
				}
				if err := os.Remove(payload); err != nil {
					t.Fatal(err)
				}
				switch replacement {
				case "fifo":
					if err := unix.Mkfifo(payload, 0o600); err != nil {
						t.Fatal(err)
					}
				case "device":
					if err := unix.Mknod(payload, unix.S_IFCHR|0o600, int(unix.Mkdev(1, 3))); err != nil {
						if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
							t.Skipf("mknod unavailable: %v", err)
						}
						t.Fatal(err)
					}
				}
			}
			destination := filepath.Join(t.TempDir(), "download")

			err = client.DownloadSkill(context.Background(), testSkillRef, destination)

			assertUnsafeNoDownloadResidue(t, err, destination)
			if dataOpened {
				t.Fatal("swapped special node was opened for data access")
			}
		})
	}
}

func TestDownloadAgentRejectsFIFOSwapBeforeAnyDataOpen(t *testing.T) {
	root := t.TempDir()
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	source := filepath.Join(root, "agents", ref.Name, ref.Version, "agent.yaml")
	writeTestFile(t, source, "agent: safe")
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	dataOpened := false
	client.afterSnapshotDataOpen = func(string) { dataOpened = true }
	client.beforeSnapshotFileOpen = func(string) {
		if err := os.Remove(source); err != nil {
			t.Fatal(err)
		}
		if err := unix.Mkfifo(source, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	destination := filepath.Join(t.TempDir(), "agent.yaml")

	err = client.DownloadAgent(context.Background(), ref, destination)

	assertUnsafeNoDownloadResidue(t, err, destination)
	if dataOpened {
		t.Fatal("swapped FIFO was opened for data access")
	}
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
