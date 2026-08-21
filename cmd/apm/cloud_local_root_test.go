package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidatePhysicalLocalRootWithInspectorRejectsUnsafeComponents(t *testing.T) {
	t.Parallel()

	root := filepath.Join(string(filepath.Separator), "physical-root-test", "resources")
	ancestor := filepath.Dir(root)
	inspectFailure := errors.New("inspection failed")
	tests := []struct {
		name       string
		inspection localRootComponentInspection
		inspectErr error
		want       string
	}{
		{
			name:       "reparse ancestor",
			inspection: localRootComponentInspection{directory: true, reparsePoint: true},
			want:       "reparse point",
		},
		{
			name:       "non-directory ancestor",
			inspection: localRootComponentInspection{},
			want:       "not a directory",
		},
		{
			name:       "inspection failure",
			inspection: localRootComponentInspection{directory: true},
			inspectErr: inspectFailure,
			want:       inspectFailure.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validatePhysicalLocalRootWithInspector(root, func(component string) (localRootComponentInspection, error) {
				if component == ancestor {
					return tt.inspection, tt.inspectErr
				}
				return localRootComponentInspection{directory: true}, nil
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validatePhysicalLocalRootWithInspector() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidatePhysicalLocalRootWithInspectorVisitsRootToLeaf(t *testing.T) {
	t.Parallel()

	root := filepath.Join(string(filepath.Separator), "physical-root-test", "nested", "resources")
	want := []string{
		string(filepath.Separator),
		filepath.Join(string(filepath.Separator), "physical-root-test"),
		filepath.Join(string(filepath.Separator), "physical-root-test", "nested"),
		root,
	}
	var visited []string
	err := validatePhysicalLocalRootWithInspector(root, func(component string) (localRootComponentInspection, error) {
		visited = append(visited, component)
		return localRootComponentInspection{directory: true}, nil
	})
	if err != nil {
		t.Fatalf("validatePhysicalLocalRootWithInspector() error = %v", err)
	}
	if !reflect.DeepEqual(visited, want) {
		t.Fatalf("visited components = %v, want %v", visited, want)
	}

	terminalErr := validatePhysicalLocalRootWithInspector(root, func(component string) (localRootComponentInspection, error) {
		if component == root {
			return localRootComponentInspection{directory: true, reparsePoint: true}, nil
		}
		return localRootComponentInspection{directory: true}, nil
	})
	if terminalErr == nil {
		t.Fatal("terminal reparse point error = nil")
	}
	if !strings.Contains(terminalErr.Error(), fmt.Sprintf("%q", root)) {
		t.Fatalf("terminal reparse point error = %q, want component context", terminalErr)
	}
}
