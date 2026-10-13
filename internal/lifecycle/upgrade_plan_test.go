package lifecycle

import (
	"errors"
	"reflect"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/version"
)

// groupsWith 构造单个类别的清单分组，便于表驱动用例表达「本地装了什么」。
func groupsWith(powerType domain.PowerType, refs ...domain.PowerRef) domain.InstalledPowerGroups {
	groups := domain.InstalledPowerGroups{}
	powers := make([]domain.InstalledPower, 0, len(refs))
	for _, ref := range refs {
		powers = append(powers, domain.InstalledPower{Ref: ref})
	}
	groups[powerType] = powers
	return groups
}

func skillRef(name, version string) domain.PowerRef {
	return domain.PowerRef{Name: name, Type: domain.PowerTypeSkill, Version: version}
}

func catalogEntries(refs ...domain.PowerRef) []domain.CatalogEntry {
	entries := make([]domain.CatalogEntry, 0, len(refs))
	for _, ref := range refs {
		entries = append(entries, catalogEntryForInstallTest(ref, "planner fixture"))
	}
	return entries
}

func TestPlanUpgradeDecidesEveryManifestAgainstLatestCatalogVersion(t *testing.T) {
	tests := []struct {
		name          string
		groups        domain.InstalledPowerGroups
		entries       []domain.CatalogEntry
		wantAction    upgradePlanAction
		wantTarget    string
		wantReason    string
		wantEntryName string
	}{
		{
			name:          "newer catalog version upgrades",
			groups:        groupsWith(domain.PowerTypeSkill, skillRef("demo", "v0.0.1")),
			entries:       catalogEntries(skillRef("demo", "v0.0.1"), skillRef("demo", "v0.1.0")),
			wantAction:    planActionUpgrade,
			wantTarget:    "v0.1.0",
			wantEntryName: "demo",
		},
		{
			name:       "identical version is up to date",
			groups:     groupsWith(domain.PowerTypeSkill, skillRef("demo", "v0.1.0")),
			entries:    catalogEntries(skillRef("demo", "v0.0.1"), skillRef("demo", "v0.1.0")),
			wantAction: planActionUpToDate,
			wantTarget: "v0.1.0",
		},
		{
			name:       "newer local version is skipped without downgrade",
			groups:     groupsWith(domain.PowerTypeSkill, skillRef("demo", "v0.2.0")),
			entries:    catalogEntries(skillRef("demo", "v0.1.0")),
			wantAction: planActionSkip,
			wantTarget: "v0.2.0",
			wantReason: cli.UpgradeSkipNewerLocal,
		},
		{
			name:       "absent from catalog is skipped",
			groups:     groupsWith(domain.PowerTypeSkill, skillRef("gone", "v0.1.0")),
			entries:    catalogEntries(skillRef("other", "v0.1.0")),
			wantAction: planActionSkip,
			wantTarget: "v0.1.0",
			wantReason: cli.UpgradeSkipNotInCatalog,
		},
		{
			// MCP 的版本切换要用旧版本目录条目做回滚，旧条目下架时必须跳过而非执行期失败。
			name: "withdrawn MCP version is skipped because replacement needs its catalog entry",
			groups: groupsWith(domain.PowerTypeMCP, domain.PowerRef{
				Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v0.0.1",
			}),
			entries: catalogEntries(domain.PowerRef{
				Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v0.1.0",
			}),
			wantAction: planActionSkip,
			wantTarget: "v0.0.1",
			wantReason: cli.UpgradeSkipNotInCatalog,
		},
		{
			// Skill 的替换路径不需要旧目录条目，旧版本下架不应阻断升级。
			name:          "withdrawn skill version still upgrades",
			groups:        groupsWith(domain.PowerTypeSkill, skillRef("demo", "v0.0.1")),
			entries:       catalogEntries(skillRef("demo", "v0.1.0")),
			wantAction:    planActionUpgrade,
			wantTarget:    "v0.1.0",
			wantEntryName: "demo",
		},
		{
			name:          "highest semver wins over lexicographic order",
			groups:        groupsWith(domain.PowerTypeSkill, skillRef("demo", "v0.0.1")),
			entries:       catalogEntries(skillRef("demo", "v0.9.0"), skillRef("demo", "v0.10.0"), skillRef("demo", "v0.2.0")),
			wantAction:    planActionUpgrade,
			wantTarget:    "v0.10.0",
			wantEntryName: "demo",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := catalogSnapshotForInstallTest(t, test.entries)
			plans := planUpgrade(domain.AgentCodex, test.groups, snapshot, version.NewSelector(), nil)

			if len(plans) != 1 {
				t.Fatalf("planUpgrade() = %#v, want exactly one plan", plans)
			}
			plan := plans[0]
			if plan.action != test.wantAction {
				t.Fatalf("action = %v, want %v", plan.action, test.wantAction)
			}
			if plan.target.Version != test.wantTarget {
				t.Fatalf("target version = %q, want %q", plan.target.Version, test.wantTarget)
			}
			if plan.reason != test.wantReason {
				t.Fatalf("reason = %q, want %q", plan.reason, test.wantReason)
			}
			if plan.agent != domain.AgentCodex {
				t.Fatalf("agent = %q, want codex", plan.agent)
			}
			if test.wantAction == planActionUpgrade {
				if plan.entry.Ref != plan.target {
					t.Fatalf("entry ref = %+v, want planned target %+v", plan.entry.Ref, plan.target)
				}
				if plan.entry.Description == "" {
					t.Fatal("upgrade plan must carry the full catalog entry")
				}
			}
		})
	}
}

func TestPlanUpgradeFiltersByRequestedTypes(t *testing.T) {
	groups := domain.InstalledPowerGroups{
		domain.PowerTypeSkill: {domain.InstalledPower{Ref: skillRef("demo", "v0.0.1")}},
		domain.PowerTypeMCP:   {domain.InstalledPower{Ref: domain.PowerRef{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v0.0.1"}}},
	}
	snapshot := catalogSnapshotForInstallTest(t, catalogEntries(
		skillRef("demo", "v0.1.0"),
		domain.PowerRef{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v0.1.0"},
	))

	plans := planUpgrade(domain.AgentCodex, groups, snapshot, version.NewSelector(), []domain.PowerType{domain.PowerTypeMCP})
	if len(plans) != 1 || plans[0].target.Type != domain.PowerTypeMCP {
		t.Fatalf("planUpgrade(mcp only) = %#v, want only the MCP plan", plans)
	}
}

func TestPlanUpgradeOrdersTypesAndKeepsManifestOrder(t *testing.T) {
	groups := domain.InstalledPowerGroups{
		domain.PowerTypeExpert: {domain.InstalledPower{Ref: domain.PowerRef{Name: "agent-one", Type: domain.PowerTypeExpert, Version: "v0.0.1"}}},
		domain.PowerTypeSkill: {
			domain.InstalledPower{Ref: skillRef("alpha", "v0.0.1")},
			domain.InstalledPower{Ref: skillRef("beta", "v0.0.1")},
		},
		domain.PowerTypeMCP: {domain.InstalledPower{Ref: domain.PowerRef{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v0.0.1"}}},
	}
	snapshot := catalogSnapshotForInstallTest(t, catalogEntries(
		skillRef("alpha", "v0.1.0"), skillRef("beta", "v0.1.0"),
		domain.PowerRef{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v0.1.0"},
		domain.PowerRef{Name: "agent-one", Type: domain.PowerTypeExpert, Version: "v0.1.0"},
	))

	plans := planUpgrade(domain.AgentCodex, groups, snapshot, version.NewSelector(), nil)
	wantOrder := []struct {
		powerType domain.PowerType
		name      string
	}{
		{domain.PowerTypeSkill, "alpha"},
		{domain.PowerTypeSkill, "beta"},
		{domain.PowerTypeMCP, "filesystem"},
		{domain.PowerTypeExpert, "agent-one"},
	}
	if len(plans) != len(wantOrder) {
		t.Fatalf("planUpgrade() = %#v, want %d plans", plans, len(wantOrder))
	}
	for index, want := range wantOrder {
		if plans[index].target.Type != want.powerType || plans[index].target.Name != want.name {
			t.Fatalf("plan[%d] = %s/%s, want %s/%s", index,
				plans[index].target.Type, plans[index].target.Name, want.powerType, want.name)
		}
	}
}

func TestPlanUpgradeHandlesEmptyAndDuplicateManifestEntries(t *testing.T) {
	t.Run("empty manifest", func(t *testing.T) {
		snapshot := catalogSnapshotForInstallTest(t, catalogEntries(skillRef("demo", "v0.1.0")))
		if plans := planUpgrade(domain.AgentCodex, domain.InstalledPowerGroups{}, snapshot, version.NewSelector(), nil); len(plans) != 0 {
			t.Fatalf("planUpgrade(empty) = %#v, want no plans", plans)
		}
	})

	t.Run("duplicate entries each produce a plan", func(t *testing.T) {
		groups := groupsWith(domain.PowerTypeSkill, skillRef("demo", "v0.0.1"), skillRef("demo", "v0.0.1"))
		snapshot := catalogSnapshotForInstallTest(t, catalogEntries(skillRef("demo", "v0.1.0")))
		plans := planUpgrade(domain.AgentCodex, groups, snapshot, version.NewSelector(), nil)

		if len(plans) != 2 {
			t.Fatalf("planUpgrade(duplicates) = %#v, want two plans", plans)
		}
		for index, plan := range plans {
			if plan.action != planActionUpgrade || plan.target.Version != "v0.1.0" {
				t.Fatalf("plan[%d] = %#v, want an upgrade to v0.1.0", index, plan)
			}
		}
	})
}

func TestPlanUpgradeKeepsCurrentAndTargetDistinct(t *testing.T) {
	groups := groupsWith(domain.PowerTypeSkill, skillRef("demo", "v0.0.1"))
	snapshot := catalogSnapshotForInstallTest(t, catalogEntries(skillRef("demo", "v0.1.0")))

	plans := planUpgrade(domain.AgentCodex, groups, snapshot, version.NewSelector(), nil)
	if len(plans) != 1 {
		t.Fatalf("planUpgrade() = %#v, want one plan", plans)
	}
	plan := plans[0]
	if plan.current != skillRef("demo", "v0.0.1") {
		t.Fatalf("current = %+v, want manifest version", plan.current)
	}
	if plan.target != skillRef("demo", "v0.1.0") {
		t.Fatalf("target = %+v, want catalog version", plan.target)
	}
}

func TestPlanUpgradeDoesNotMutateManifestGroups(t *testing.T) {
	groups := groupsWith(domain.PowerTypeSkill, skillRef("demo", "v0.0.1"))
	original := groupsWith(domain.PowerTypeSkill, skillRef("demo", "v0.0.1"))
	snapshot := catalogSnapshotForInstallTest(t, catalogEntries(skillRef("demo", "v0.1.0")))

	planUpgrade(domain.AgentCodex, groups, snapshot, version.NewSelector(), nil)

	if !reflect.DeepEqual(groups, original) {
		t.Fatalf("planUpgrade() mutated manifest groups: %#v, want %#v", groups, original)
	}
}

func TestPlanUpgradeIsDeterministic(t *testing.T) {
	groups := domain.InstalledPowerGroups{
		domain.PowerTypeSkill: {domain.InstalledPower{Ref: skillRef("demo", "v0.0.1")}},
		domain.PowerTypeMCP:   {domain.InstalledPower{Ref: domain.PowerRef{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v0.0.1"}}},
	}
	snapshot := catalogSnapshotForInstallTest(t, catalogEntries(skillRef("demo", "v0.1.0")))

	first := planUpgrade(domain.AgentCodex, groups, snapshot, version.NewSelector(), nil)
	second := planUpgrade(domain.AgentCodex, groups, snapshot, version.NewSelector(), nil)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("planUpgrade() is not deterministic: %#v vs %#v", first, second)
	}
}

func TestPlanUpgradeTreatsSelectorFailureAsSkip(t *testing.T) {
	groups := groupsWith(domain.PowerTypeSkill, skillRef("demo", "v0.0.1"))
	snapshot := catalogSnapshotForInstallTest(t, catalogEntries(skillRef("demo", "v0.1.0")))
	selector := &installFakeVersions{err: errors.New("selector failure")}

	plans := planUpgrade(domain.AgentCodex, groups, snapshot, selector, nil)
	if len(plans) != 1 || plans[0].action != planActionSkip || plans[0].reason != cli.UpgradeSkipNotInCatalog {
		t.Fatalf("planUpgrade(selector failure) = %#v, want a skip", plans)
	}
}

func TestCatalogSnapshotDecodeKeepsPerTypeGroupsIndependent(t *testing.T) {
	// planner 依赖 snapshot.List(powerType) 只返回该类别；同名跨类别不得互相干扰。
	snapshot := catalogSnapshotForInstallTest(t, catalogEntries(
		skillRef("shared", "v0.1.0"),
		domain.PowerRef{Name: "shared", Type: domain.PowerTypeMCP, Version: "v0.1.0"},
	))
	if len(snapshot.List(domain.PowerTypeSkill)) != 1 || len(snapshot.List(domain.PowerTypeMCP)) != 1 {
		t.Fatalf("snapshot groups = skill %d, mcp %d; want 1 each",
			len(snapshot.List(domain.PowerTypeSkill)), len(snapshot.List(domain.PowerTypeMCP)))
	}

	groups := groupsWith(domain.PowerTypeSkill, skillRef("shared", "v0.0.1"))
	plans := planUpgrade(domain.AgentCodex, groups, snapshot, version.NewSelector(), nil)
	if len(plans) != 1 || plans[0].target.Type != domain.PowerTypeSkill {
		t.Fatalf("planUpgrade() = %#v, want the skill plan only", plans)
	}
}
