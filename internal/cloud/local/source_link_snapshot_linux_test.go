//go:build linux

package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"apm/internal/cloud"
	"golang.org/x/sys/unix"
)

func TestLinkSnapshotBackendCapturesValidTreeAndFile(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "skill")
	writeTestFile(t, filepath.Join(source, "payload.md"), "payload")

	snapshot, err := capturePhysicalTreeLinkSnapshot(context.Background(), root, source, nil, nil, nil)
	if err != nil {
		t.Fatalf("capturePhysicalTreeLinkSnapshot() error = %v", err)
	}
	if err := snapshot.requireRegularFile("SKILL.md"); err != nil {
		t.Fatal(err)
	}

	fileSource := filepath.Join(root, "agents", "demo", "v1.0.0", "agent.yaml")
	writeTestFile(t, fileSource, "agent")
	contents, _, err := capturePhysicalFileLinkSnapshot(context.Background(), root, fileSource, maxSingleSize, nil, nil)
	if err != nil || string(contents) != "agent" {
		t.Fatalf("capturePhysicalFileLinkSnapshot() = %q, %v", contents, err)
	}
	assertNoLinkSnapshotWorkspaceResidue(t, root)
}

func TestLinkSnapshotBackendUsesWritableSameFilesystemParentForReadOnlyRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "resources")
	source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "skill")
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	snapshot, err := capturePhysicalTreeLinkSnapshot(context.Background(), root, source, nil, nil, nil)

	if err != nil {
		t.Fatalf("capture from read-only resource root: %v", err)
	}
	if err := snapshot.requireRegularFile("SKILL.md"); err != nil {
		t.Fatal(err)
	}
	assertNoLinkSnapshotWorkspaceResidue(t, root)
}

func TestLinkSnapshotBackendPinsConfiguredRootAcrossSymlinkedAncestor(t *testing.T) {
	parent := t.TempDir()
	physicalParent := filepath.Join(parent, "physical")
	root := filepath.Join(physicalParent, "resources")
	source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "skill")
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(physicalParent, alias); err != nil {
		t.Fatal(err)
	}
	configuredRoot := filepath.Join(alias, "resources")
	configuredSource := filepath.Join(configuredRoot, "skills", testSkillRef.Name, testSkillRef.Version)

	snapshot, err := capturePhysicalTreeLinkSnapshot(context.Background(), configuredRoot, configuredSource, nil, nil, nil)

	if err != nil {
		t.Fatalf("capture through configured root ancestor: %v", err)
	}
	if err := snapshot.requireRegularFile("SKILL.md"); err != nil {
		t.Fatal(err)
	}
	assertNoLinkSnapshotWorkspaceResidue(t, root)
}

func TestLinkSnapshotBackendRejectsFIFOSwapBeforeDataOpen(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "skill")
	payload := filepath.Join(source, "payload.md")
	writeTestFile(t, payload, "payload")
	dataOpened := false

	_, err := capturePhysicalTreeLinkSnapshot(
		context.Background(),
		root,
		source,
		nil,
		func(relative string) {
			if relative != "payload.md" {
				return
			}
			if err := os.Remove(payload); err != nil {
				t.Fatal(err)
			}
			if err := unix.Mkfifo(payload, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		func(relative string) {
			if relative == "payload.md" {
				dataOpened = true
			}
		},
	)

	if !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("capturePhysicalTreeLinkSnapshot() error = %v, want ErrUnsafeContent", err)
	}
	if dataOpened {
		t.Fatal("swapped FIFO was opened for data access")
	}
	assertNoLinkSnapshotWorkspaceResidue(t, root)
}

func TestLinkSnapshotBackendRejectsRegularSwapBeforeDataOpen(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "skill")
	payload := filepath.Join(source, "payload.md")
	writeTestFile(t, payload, "validated")
	dataOpened := false

	_, err := capturePhysicalTreeLinkSnapshot(
		context.Background(),
		root,
		source,
		nil,
		func(relative string) {
			if relative != "payload.md" {
				return
			}
			if err := os.Rename(payload, payload+".original"); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, payload, "replacement")
		},
		func(relative string) {
			if relative == "payload.md" {
				dataOpened = true
			}
		},
	)

	if !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("capturePhysicalTreeLinkSnapshot() error = %v, want ErrUnsafeContent", err)
	}
	if dataOpened {
		t.Fatal("replacement regular file reached the data-open boundary")
	}
	assertNoLinkSnapshotWorkspaceResidue(t, root)
}

func TestLinkSnapshotBackendRejectsHardlinkAndSymlink(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "hardlink",
			mutate: func(t *testing.T, source string) {
				if err := os.Link(source, source+".link"); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink",
			mutate: func(t *testing.T, source string) {
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), source); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "agents", "demo", "v1.0.0", "agent.yaml")
			writeTestFile(t, source, "agent")
			test.mutate(t, source)

			_, _, err := capturePhysicalFileLinkSnapshot(context.Background(), root, source, maxSingleSize, nil, nil)

			if !errors.Is(err, cloud.ErrUnsafeContent) {
				t.Fatalf("capturePhysicalFileLinkSnapshot() error = %v, want ErrUnsafeContent", err)
			}
			assertNoLinkSnapshotWorkspaceResidue(t, root)
		})
	}
}

func TestLinkSnapshotBackendSerializesConcurrentPinning(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "agents", "demo", "v1.0.0", "agent.yaml")
	writeTestFile(t, source, "agent")
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstResult := make(chan error, 1)
	go func() {
		_, _, err := capturePhysicalFileLinkSnapshot(
			context.Background(),
			root,
			source,
			maxSingleSize,
			func(string) {
				close(firstEntered)
				<-releaseFirst
			},
			nil,
		)
		firstResult <- err
	}()
	<-firstEntered

	secondStarted := make(chan struct{})
	secondEntered := make(chan struct{})
	secondResult := make(chan error, 1)
	go func() {
		close(secondStarted)
		_, _, err := capturePhysicalFileLinkSnapshot(
			context.Background(),
			root,
			source,
			maxSingleSize,
			func(string) { close(secondEntered) },
			nil,
		)
		secondResult <- err
	}()
	<-secondStarted

	overlapped := false
	select {
	case <-secondEntered:
		overlapped = true
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstResult; err != nil {
		t.Fatalf("first capture error = %v", err)
	}
	if err := <-secondResult; err != nil {
		t.Fatalf("second capture error = %v", err)
	}
	if overlapped {
		t.Fatal("concurrent capture entered the same hard-link pinning window")
	}
	assertNoLinkSnapshotWorkspaceResidue(t, root)
}

func assertNoLinkSnapshotWorkspaceResidue(t *testing.T, resourcesRoot string) {
	t.Helper()
	for _, directory := range []string{filepath.Dir(resourcesRoot), resourcesRoot} {
		matches, err := filepath.Glob(filepath.Join(directory, linkSnapshotWorkspacePrefix+"*"))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			t.Fatalf("link snapshot workspace residue = %v", matches)
		}
	}
}
