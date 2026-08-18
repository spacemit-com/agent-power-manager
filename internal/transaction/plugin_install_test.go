package transaction

import (
	"context"

	"apm/internal/adapter"
	"apm/internal/domain"
)

type pluginMutationAnswer struct {
	result adapter.PluginMutationResult
	err    error
}

type recordingPluginAdapter struct {
	events        *[]string
	mode          domain.PluginDeploymentMode
	facts         map[string]adapter.PluginFact
	inspectErr    error
	addAnswers    []pluginMutationAnswer
	removeAnswers []pluginMutationAnswer
	added         []adapter.PluginTarget
	removed       []adapter.PluginTarget
	targets       map[string]string
}

func (a *recordingPluginAdapter) DeploymentMode() domain.PluginDeploymentMode { return a.mode }
func (a *recordingPluginAdapter) Inspect(_ context.Context, name string) (adapter.PluginFact, error) {
	*a.events = append(*a.events, "adapter:inspect:"+name)
	return a.facts[name], a.inspectErr
}
func (a *recordingPluginAdapter) Add(_ context.Context, target adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	*a.events = append(*a.events, "adapter:add:"+target.ActualName)
	a.added = append(a.added, target)
	answer := a.addAnswers[0]
	a.addAnswers = a.addAnswers[1:]
	return answer.result, answer.err
}
func (a *recordingPluginAdapter) Remove(_ context.Context, target adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	*a.events = append(*a.events, "adapter:remove:"+target.ActualName)
	a.removed = append(a.removed, target)
	answer := a.removeAnswers[0]
	a.removeAnswers = a.removeAnswers[1:]
	return answer.result, answer.err
}
func (a *recordingPluginAdapter) TargetPath(name string) (string, error) {
	if target, ok := a.targets[name]; ok {
		return target, nil
	}
	return "", adapter.ErrUnsupportedAdapter
}

type pluginInstallFixture struct {
	events    []string
	journals  *recordingJournalStore
	manifests *recordingManifestWriter
	adapter   *recordingPluginAdapter
	service   *Service
	request   PluginInstallRequest
}

func newPluginInstallFixture() *pluginInstallFixture {
	f := &pluginInstallFixture{}
	f.journals = &recordingJournalStore{events: &f.events, fail: map[string]error{}}
	f.manifests = &recordingManifestWriter{events: &f.events}
	f.adapter = &recordingPluginAdapter{
		events: &f.events, mode: domain.PluginDeploymentFilesystem,
		facts:      map[string]adapter.PluginFact{"demo-v2.0.0": {Presence: adapter.PluginAbsent, ActualName: "demo-v2.0.0"}},
		addAnswers: []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}},
	}
	f.service = newService(f.manifests, f.journals)
	f.service.newID = func() (string, error) { return "00112233445566778899aabbccddeeff", nil }
	f.request = PluginInstallRequest{
		Agent:   domain.AgentHermes,
		Power:   domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v2.0.0"},
		Adapter: f.adapter,
		Target:  adapter.PluginTarget{ActualName: "demo-v2.0.0", Selector: "demo-v2.0.0"},
	}
	return f
}
