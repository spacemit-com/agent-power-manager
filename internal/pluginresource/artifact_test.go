package pluginresource_test

import (
	"testing"

	"apm/internal/domain"
	"apm/internal/pluginresource"
)

func TestArtifactValidateRequiresAbsoluteMarketplaceSourceForMarketplaceAgents(t *testing.T) {
	for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode} {
		t.Run(string(agent), func(t *testing.T) {
			artifact := validArtifact(agent)
			if err := artifact.Validate(); err != nil {
				t.Fatalf("Validate() = %v", err)
			}

			artifact.MarketplaceSource = "relative/marketplace"
			if err := artifact.Validate(); err == nil {
				t.Fatal("Validate() error = nil for relative marketplace source")
			}
		})
	}
}

func TestArtifactValidateRequiresEmptyMarketplaceForHermes(t *testing.T) {
	artifact := validArtifact(domain.AgentHermes)
	if err := artifact.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}

	for _, update := range []func(*pluginresource.Artifact){
		func(artifact *pluginresource.Artifact) { artifact.MarketplaceName = "apm-demo" },
		func(artifact *pluginresource.Artifact) { artifact.MarketplaceSource = "/srv/apm-marketplace" },
	} {
		invalid := artifact
		update(&invalid)
		if err := invalid.Validate(); err == nil {
			t.Fatalf("Validate() error = nil for %#v", invalid)
		}
	}
}

func validArtifact(agent domain.AgentID) pluginresource.Artifact {
	artifact := pluginresource.Artifact{
		Agent:      agent,
		Power:      domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v0.1.0"},
		ActualName: "demo-v0.1.0",
		Root:       "/srv/apm/plugins/demo-v0.1.0",
		PluginPath: "/srv/apm/plugins/demo-v0.1.0/plugin.json",
	}
	if agent != domain.AgentHermes {
		artifact.MarketplaceName = "apm-demo"
		artifact.MarketplaceSource = "/srv/apm/marketplaces/apm-demo"
	}
	return artifact
}
