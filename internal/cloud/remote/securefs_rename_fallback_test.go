package remote

import (
	"errors"
	"reflect"
	"testing"
)

func TestLinkThenUnlinkNoReplaceNeverRollsBackDestination(t *testing.T) {
	removeErr := errors.New("remove staged source failed")
	var linked [][2]string
	var removed []string
	err := linkThenUnlinkNoReplace(
		"temporary",
		"agent.yaml",
		func(source, destination string) error {
			linked = append(linked, [2]string{source, destination})
			return nil
		},
		func(name string) error {
			removed = append(removed, name)
			return removeErr
		},
	)
	if !errors.Is(err, removeErr) {
		t.Fatalf("linkThenUnlinkNoReplace() error = %v, want source removal error", err)
	}
	if want := [][2]string{{"temporary", "agent.yaml"}}; !reflect.DeepEqual(linked, want) {
		t.Fatalf("link calls = %#v, want %#v", linked, want)
	}
	if want := []string{"temporary"}; !reflect.DeepEqual(removed, want) {
		t.Fatalf("remove calls = %#v, want %#v (only staged source cleanup)", removed, want)
	}
}
