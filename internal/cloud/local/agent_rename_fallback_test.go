package local

import (
	"errors"
	"reflect"
	"testing"
)

func TestLinkAgentNoReplaceRollsBackDestinationWhenSourceRemovalFails(t *testing.T) {
	sourceRemovalErr := errors.New("source removal failed")
	rollbackErr := errors.New("rollback failed")
	var linked [][2]string
	var removed []string
	link := func(source, destination string) error {
		linked = append(linked, [2]string{source, destination})
		return nil
	}
	remove := func(path string) error {
		removed = append(removed, path)
		if path == "source" {
			return sourceRemovalErr
		}
		return rollbackErr
	}

	err := linkAgentNoReplace("source", "destination", link, remove)

	if !errors.Is(err, sourceRemovalErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("linkAgentNoReplace() error = %v, want source removal and rollback errors", err)
	}
	if want := [][2]string{{"source", "destination"}}; !reflect.DeepEqual(linked, want) {
		t.Fatalf("link calls = %#v, want %#v", linked, want)
	}
	if want := []string{"source", "destination"}; !reflect.DeepEqual(removed, want) {
		t.Fatalf("remove calls = %#v, want %#v", removed, want)
	}
}
