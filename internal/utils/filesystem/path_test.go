package filesystem

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePhysicalPathPreservesMissingSuffix(t *testing.T) {
	root := t.TempDir()
	physical := filepath.Join(root, "physical")
	if err := os.Mkdir(physical, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(physical, link); err != nil {
		t.Fatal(err)
	}

	got, err := ResolvePhysicalPath(filepath.Join(link, "missing", "leaf"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(physical, "missing", "leaf"); got != want {
		t.Fatalf("ResolvePhysicalPath() = %q, want %q", got, want)
	}
}

func TestResolvePhysicalPathRejectsSymlinkLoop(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.Symlink(second, first); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(first, second); err != nil {
		t.Fatal(err)
	}

	if _, err := ResolvePhysicalPath(filepath.Join(first, "leaf")); err == nil {
		t.Fatal("ResolvePhysicalPath() error = nil, want symlink loop error")
	}
}

func TestPathEqualOrWithinUsesPathSegments(t *testing.T) {
	root := t.TempDir()
	tests := map[string]struct {
		parent string
		path   string
		want   bool
	}{
		"equal":      {parent: root, path: root, want: true},
		"descendant": {parent: root, path: filepath.Join(root, "child", "leaf"), want: true},
		"parent":     {parent: filepath.Join(root, "child"), path: root, want: false},
		"sibling":    {parent: filepath.Join(root, "stage"), path: filepath.Join(root, "stage-agent"), want: false},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := PathEqualOrWithin(test.parent, test.path)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("PathEqualOrWithin(%q, %q) = %v, want %v", test.parent, test.path, got, test.want)
			}
		})
	}
}
