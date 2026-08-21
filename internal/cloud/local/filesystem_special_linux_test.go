//go:build linux

package local

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDownloadSkillRejectsLinuxSpecialNodes(t *testing.T) {
	tests := []struct {
		name   string
		create func(*testing.T, string)
	}{
		{
			name: "fifo",
			create: func(t *testing.T, path string) {
				if err := unix.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "socket",
			create: func(t *testing.T, path string) {
				listener, err := net.Listen("unix", path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = listener.Close() })
			},
		},
		{
			name: "device",
			create: func(t *testing.T, path string) {
				err := unix.Mknod(path, unix.S_IFCHR|0o600, int(unix.Mkdev(1, 3)))
				if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
					t.Skipf("creating a device node is not permitted: %v", err)
				}
				if err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
			writeTestFile(t, filepath.Join(source, "SKILL.md"), "skill")
			test.create(t, filepath.Join(source, "special"))
			client, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "download")

			err = client.DownloadSkill(context.Background(), testSkillRef, destination)

			assertUnsafeNoDownloadResidue(t, err, destination)
			if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("destination stat error = %v, want absent", statErr)
			}
		})
	}
}
