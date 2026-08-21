package remote

import (
	"archive/tar"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
)

var remoteSkillRef = domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo-skill", Version: "v1.2.3"}

func TestDownloadSkillPublishesVerifiedArchiveWithRequiredMarkers(t *testing.T) {
	contents := testTarGzBytes(t,
		testTarEntry{name: "SKILL.md", body: []byte("# Demo\n")},
		testTarEntry{name: "skill-card.md", body: []byte("summary\n")},
		testTarEntry{name: "scripts", typeflag: tar.TypeDir},
		testTarEntry{name: "scripts/run.sh", mode: 0o755, body: []byte("#!/bin/sh\n")},
	)
	var accepts []string
	client := newContentClient(t, remoteSkillRef, contents, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			accepts = append(accepts, r.Header.Get("Accept"))
		}
		serveValidContent(t, w, r, remoteSkillRef, contents)
	})
	destination := filepath.Join(trustedTestDir(t), "skill")
	if err := client.DownloadSkill(context.Background(), remoteSkillRef, destination); err != nil {
		t.Fatal(err)
	}
	assertFileContentsAndMode(t, filepath.Join(destination, "SKILL.md"), "# Demo\n", 0o644)
	assertFileContentsAndMode(t, filepath.Join(destination, "skill-card.md"), "summary\n", 0o644)
	assertFileContentsAndMode(t, filepath.Join(destination, "scripts", "run.sh"), "#!/bin/sh\n", 0o755)
	if len(accepts) != 1 || accepts[0] != "application/gzip" {
		t.Fatalf("content Accept headers = %v, want application/gzip", accepts)
	}
}

func TestDownloadSkillAcceptsExistingEmptyPhysicalDestination(t *testing.T) {
	contents := testTarGzBytes(t,
		testTarEntry{name: "SKILL.md", body: []byte("skill")},
		testTarEntry{name: "skill-card.md", body: []byte("card")},
	)
	client := newContentClient(t, remoteSkillRef, contents, func(w http.ResponseWriter, r *http.Request) {
		serveValidContent(t, w, r, remoteSkillRef, contents)
	})
	destination := filepath.Join(trustedTestDir(t), "skill")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := client.DownloadSkill(context.Background(), remoteSkillRef, destination); err != nil {
		t.Fatal(err)
	}
	assertFileContentsAndMode(t, filepath.Join(destination, "SKILL.md"), "skill", 0o644)
}

func TestDownloadSkillRejectsMissingOrNonRegularRootMarkers(t *testing.T) {
	tests := []struct {
		name    string
		entries []testTarEntry
	}{
		{
			name:    "missing SKILL.md",
			entries: []testTarEntry{{name: "skill-card.md", body: []byte("card")}},
		},
		{
			name:    "missing skill-card.md",
			entries: []testTarEntry{{name: "SKILL.md", body: []byte("skill")}},
		},
		{
			name: "SKILL.md is a directory",
			entries: []testTarEntry{
				{name: "SKILL.md", typeflag: tar.TypeDir},
				{name: "skill-card.md", body: []byte("card")},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contents := testTarGzBytes(t, tt.entries...)
			client := newContentClient(t, remoteSkillRef, contents, func(w http.ResponseWriter, r *http.Request) {
				serveValidContent(t, w, r, remoteSkillRef, contents)
			})
			destination := filepath.Join(trustedTestDir(t), "skill")
			err := client.DownloadSkill(context.Background(), remoteSkillRef, destination)
			if !errors.Is(err, cloud.ErrUnsafeContent) || errors.Is(err, cloud.ErrNotFound) {
				t.Fatalf("DownloadSkill() error = %v, want only ErrUnsafeContent marker classification", err)
			}
			if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("invalid Skill destination remains: %v", statErr)
			}
		})
	}
}

func TestDownloadSkillVerifiesMD5BeforeArchiveInspection(t *testing.T) {
	contents := []byte("not gzip data")
	client := newContentClient(t, remoteSkillRef, contents, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(contents)
			return
		}
		writeExactMetadata(t, w, remoteSkillRef, contents, map[string]any{"content_md5": md5Hex([]byte("different"))})
	})
	destination := filepath.Join(trustedTestDir(t), "skill")
	err := client.DownloadSkill(context.Background(), remoteSkillRef, destination)
	if !errors.Is(err, cloud.ErrIntegrity) || errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("DownloadSkill() error = %v, want MD5 ErrIntegrity before archive classification", err)
	}
	assertDirectoryEmpty(t, filepath.Dir(destination))
}

func TestDownloadSkillRejectsInvalidIdentityAndUnsafeDestinationBeforeRequest(t *testing.T) {
	var requests atomic.Int32
	client := newContentClient(t, remoteSkillRef, nil, func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	})
	parent := trustedTestDir(t)
	nonempty := filepath.Join(parent, "nonempty")
	if err := os.Mkdir(nonempty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonempty, "preserve"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	physical := trustedTestDir(t)
	linked := filepath.Join(parent, "linked")
	if err := os.Symlink(physical, linked); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		ref         domain.PowerRef
		destination string
		want        error
	}{
		{ref: domain.PowerRef{Type: domain.PowerTypePlugin, Name: remoteSkillRef.Name, Version: remoteSkillRef.Version}, destination: filepath.Join(parent, "wrong-type"), want: cloud.ErrNotFound},
		{ref: remoteSkillRef, destination: "relative", want: cloud.ErrUnsafeContent},
		{ref: remoteSkillRef, destination: parent + string(filepath.Separator) + "." + string(filepath.Separator) + "unclean", want: cloud.ErrUnsafeContent},
		{ref: remoteSkillRef, destination: filepath.Join(linked, "skill"), want: cloud.ErrUnsafeContent},
		{ref: remoteSkillRef, destination: nonempty, want: cloud.ErrUnsafeContent},
	}
	for _, tt := range tests {
		if err := client.DownloadSkill(context.Background(), tt.ref, tt.destination); !errors.Is(err, tt.want) {
			t.Fatalf("DownloadSkill(%+v, %q) error = %v, want %v", tt.ref, tt.destination, err, tt.want)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d, want invalid input rejected before network", requests.Load())
	}
	assertFileContentsAndMode(t, filepath.Join(nonempty, "preserve"), "keep", 0o600)
}
