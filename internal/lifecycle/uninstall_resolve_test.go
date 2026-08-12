package lifecycle

import (
	"reflect"
	"strings"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
)

func TestResolveUninstallPower(t *testing.T) {
	tests := []struct {
		name    string
		flags   cli.Flags
		groups  domain.PowerGroups
		want    domain.PowerRef
		found   bool
		wantErr string
	}{
		{
			name:  "exact name and inferred type",
			flags: cli.Flags{Name: "Demo"},
			groups: domain.PowerGroups{domain.PowerTypeSkill: {
				{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"},
				{Type: domain.PowerTypeSkill, Name: "Demo", Version: "v2.0.0"},
			}},
			want:  domain.PowerRef{Type: domain.PowerTypeSkill, Name: "Demo", Version: "v2.0.0"},
			found: true,
		},
		{
			name:  "explicit type does not fall back",
			flags: cli.Flags{Name: "demo", Type: "plugin"},
			groups: domain.PowerGroups{domain.PowerTypeSkill: {
				{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"},
			}},
			found: false,
		},
		{
			name:  "explicit type uses first duplicate in manifest order",
			flags: cli.Flags{Name: "demo", Type: "skill"},
			groups: domain.PowerGroups{domain.PowerTypeSkill: {
				{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.0.0"},
				{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"},
			}},
			want:  domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.0.0"},
			found: true,
		},
		{
			name:  "inferred type uses first duplicate in manifest order",
			flags: cli.Flags{Name: "demo"},
			groups: domain.PowerGroups{domain.PowerTypeSkill: {
				{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.0.0"},
				{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"},
			}},
			want:  domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.0.0"},
			found: true,
		},
		{
			name:  "same agent cross type ambiguity",
			flags: cli.Flags{Name: "demo"},
			groups: domain.PowerGroups{
				domain.PowerTypeSkill:  {{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}},
				domain.PowerTypePlugin: {{Type: domain.PowerTypePlugin, Name: "demo", Version: "v2.0.0"}},
			},
			wantErr: "multiple power types [plugin skill]; specify --type",
		},
		{
			name:  "cross type ambiguity lists each type once",
			flags: cli.Flags{Name: "demo"},
			groups: domain.PowerGroups{
				domain.PowerTypeSkill: {
					{Type: domain.PowerTypeSkill, Name: "demo", Version: "v3.0.0"},
					{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.0.0"},
				},
				domain.PowerTypePlugin: {{Type: domain.PowerTypePlugin, Name: "demo", Version: "v1.0.0"}},
			},
			wantErr: "multiple power types [plugin skill]; specify --type",
		},
		{
			name:  "no exact name",
			flags: cli.Flags{Name: "Demo"},
			groups: domain.PowerGroups{
				domain.PowerTypeSkill: {{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}},
			},
			found: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groups := clonePowerGroups(tt.groups)
			wantGroups := clonePowerGroups(groups)

			got, found, err := resolveUninstallPower(tt.flags, groups)

			if !reflect.DeepEqual(groups, wantGroups) {
				t.Fatalf("resolveUninstallPower() modified groups: got %#v, want %#v", groups, wantGroups)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("resolveUninstallPower() error = %v, want nil", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("resolveUninstallPower() error = %v, want it to contain %q", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("resolveUninstallPower() ref = %#v, want %#v", got, tt.want)
			}
			if found != tt.found {
				t.Errorf("resolveUninstallPower() found = %t, want %t", found, tt.found)
			}
		})
	}
}

func TestResolveUninstallPowerResolvesAgentsIndependently(t *testing.T) {
	agentAGroups := domain.PowerGroups{
		domain.PowerTypeSkill: {{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}},
	}
	agentBGroups := domain.PowerGroups{
		domain.PowerTypePlugin: {{Type: domain.PowerTypePlugin, Name: "demo", Version: "v2.0.0"}},
	}
	wantAgentA := clonePowerGroups(agentAGroups)
	wantAgentB := clonePowerGroups(agentBGroups)

	gotA, foundA, errA := resolveUninstallPower(cli.Flags{Name: "demo"}, agentAGroups)
	gotB, foundB, errB := resolveUninstallPower(cli.Flags{Name: "demo"}, agentBGroups)

	if errA != nil || !foundA || gotA != (domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}) {
		t.Fatalf("agent A resolution = (%#v, %t, %v), want skill match", gotA, foundA, errA)
	}
	if errB != nil || !foundB || gotB != (domain.PowerRef{Type: domain.PowerTypePlugin, Name: "demo", Version: "v2.0.0"}) {
		t.Fatalf("agent B resolution = (%#v, %t, %v), want plugin match", gotB, foundB, errB)
	}
	if !reflect.DeepEqual(agentAGroups, wantAgentA) {
		t.Fatalf("agent A groups modified: got %#v, want %#v", agentAGroups, wantAgentA)
	}
	if !reflect.DeepEqual(agentBGroups, wantAgentB) {
		t.Fatalf("agent B groups modified: got %#v, want %#v", agentBGroups, wantAgentB)
	}
}

func clonePowerGroups(groups domain.PowerGroups) domain.PowerGroups {
	clone := make(domain.PowerGroups, len(groups))
	for powerType, refs := range groups {
		clone[powerType] = append([]domain.PowerRef(nil), refs...)
	}
	return clone
}
