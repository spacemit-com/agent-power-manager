package submission

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildTarGzIsDeterministic(t *testing.T) {
	root := writeTree(t)
	first, err := BuildTarGz(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildTarGz(root)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("same directory must produce identical tar.gz bytes")
	}
}

func TestBuildTarGzChangesOnContentEdit(t *testing.T) {
	root := writeTree(t)
	first, err := BuildTarGz(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := BuildTarGz(root)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("tar.gz must change when a file changes")
	}
}

func TestBuildTarGzEntriesSortedAndReadable(t *testing.T) {
	root := writeTree(t)
	data, err := BuildTarGz(root)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	want := []string{"SKILL.md", "a.txt", "sub/", "sub/b.txt"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entry %d = %q, want %q", i, names[i], want[i])
		}
	}
}

func writeTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{"SKILL.md": "hello", "a.txt": "A", "sub/b.txt": "B"}
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
