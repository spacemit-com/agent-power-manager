package pluginresource_test

import (
	"errors"
	"testing"

	"apm/internal/domain"
	"apm/internal/pluginresource"
)

func TestSharedArtifactSelectsAgentMarketplace(t *testing.T) {
	// 捕获错误变更：共享 Artifact 再次携带 Agent 身份，或为两个 marketplace 返回不同缓存根。
	artifact := pluginresource.Artifact{
		Power:                   domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.2.3"},
		Root:                    "/cache/plugins/demo/v1.2.3",
		PluginPath:              "/cache/plugins/demo/v1.2.3/plugins/demo",
		CodexMarketplaceSource:  "/cache/plugins/demo/v1.2.3",
		ClaudeMarketplaceSource: "/cache/plugins/demo/v1.2.3",
	}
	for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode} {
		target, err := artifact.MarketplaceFor(agent)
		if err != nil || target.Name != "apm-demo" || target.Source != artifact.Root {
			t.Fatalf("MarketplaceFor(%s) = %+v, %v", agent, target, err)
		}
	}
	for _, agent := range []domain.AgentID{domain.AgentHermes, domain.AgentBibit} {
		if _, err := artifact.MarketplaceFor(agent); !errors.Is(err, pluginresource.ErrUnsupportedAgent) {
			t.Fatalf("MarketplaceFor(%s) error = %v, want ErrUnsupportedAgent", agent, err)
		}
	}
}

func TestSharedArtifactRejectsInvalidMarketplaceSources(t *testing.T) {
	// 捕获错误变更：允许 marketplace source 逃离 Artifact 根或两个 Agent 指向不同根。
	valid := pluginresource.Artifact{
		Power:                   domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.2.3"},
		Root:                    "/cache/plugins/demo/v1.2.3",
		PluginPath:              "/cache/plugins/demo/v1.2.3/plugins/demo",
		CodexMarketplaceSource:  "/cache/plugins/demo/v1.2.3",
		ClaudeMarketplaceSource: "/cache/plugins/demo/v1.2.3",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	for _, mutate := range []func(*pluginresource.Artifact){
		func(artifact *pluginresource.Artifact) { artifact.CodexMarketplaceSource = "relative/cache" },
		func(artifact *pluginresource.Artifact) {
			artifact.ClaudeMarketplaceSource = "/cache/plugins/other/v1.2.3"
		},
		func(artifact *pluginresource.Artifact) {
			artifact.PluginPath = "/cache/plugins/demo/v1.2.3/plugins/other"
		},
	} {
		artifact := valid
		mutate(&artifact)
		if err := artifact.Validate(); !errors.Is(err, pluginresource.ErrInvalidArtifact) {
			t.Fatalf("Validate(%+v) error = %v, want ErrInvalidArtifact", artifact, err)
		}
	}
}
