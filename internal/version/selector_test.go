package version_test

import (
	"reflect"
	"strings"
	"testing"

	"apm/internal/version"
)

func TestSelectorSelectsRequestedOrLatestVersion(t *testing.T) {
	tests := []struct {
		name       string
		candidates []string
		requested  string
		want       string
		wantErr    string
	}{
		{name: "latest", candidates: []string{"v1.9.0", "v1.10.0", "v0.9.0"}, want: "v1.10.0"},
		{name: "exact", candidates: []string{"v1.0.0", "v2.0.0"}, requested: "v1.0.0", want: "v1.0.0"},
		{name: "empty", wantErr: "no candidate versions"},
		{name: "missing exact", candidates: []string{"v1.0.0"}, requested: "v2.0.0", wantErr: `version "v2.0.0" not found`},
	}
	selector := version.NewSelector()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := append([]string(nil), tt.candidates...)
			got, err := selector.Select(tt.candidates, tt.requested)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Select() error = %v, want %q", err, tt.wantErr)
				}
			} else if err != nil || got != tt.want {
				t.Fatalf("Select() = %q, %v; want %q, nil", got, err, tt.want)
			}
			if !reflect.DeepEqual(tt.candidates, before) {
				t.Fatalf("Select() changed candidates: got %v, want %v", tt.candidates, before)
			}
		})
	}
}
