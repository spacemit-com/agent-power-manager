package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

// recordingSubmissionClient 记录 Submit/GetSubmission/ListSubmissions 调用并返回预设结果；
// 其余 Cloud 方法复用 stubCloudClient 的空实现。
type recordingSubmissionClient struct {
	stubCloudClient
	submitCalls []domain.SubmissionRequest
	getCalls    []string
	listCalls   []domain.SubmissionListQuery
	receipt     domain.SubmissionResult
	detail      domain.Submission
	list        domain.SubmissionList
	submitErr   error
	getErr      error
	listErr     error
}

func (c *recordingSubmissionClient) Submit(_ context.Context, request domain.SubmissionRequest) (domain.SubmissionResult, error) {
	c.submitCalls = append(c.submitCalls, request)
	return c.receipt, c.submitErr
}

func (c *recordingSubmissionClient) GetSubmission(_ context.Context, id string) (domain.Submission, error) {
	c.getCalls = append(c.getCalls, id)
	return c.detail, c.getErr
}

func (c *recordingSubmissionClient) ListSubmissions(_ context.Context, query domain.SubmissionListQuery) (domain.SubmissionList, error) {
	c.listCalls = append(c.listCalls, query)
	return c.list, c.listErr
}

// lifecycleAgentYAML 是 submit 生命周期测试共用的最小合法 V0.5 agent.yaml：
// 目录型 Agent 资源的根闭集只允许 agent.yaml 与可选 assets/，且必须携带
// name/soul 等稳定字段（V0.4 单文件形式已被 ValidateDirectory 拒绝）；
// id 与 name 同值，符合 ADR-033 的资源创作约定。
const lifecycleAgentYAML = "schema_version: 1\nagent:\n  id: demo-agent\n  name: demo-agent\n  version: v1.0.0\n  description: demo\n  soul: help safely\n"

// lifecycleAgentYAMLMismatchedID 是 agent.id 与 agent.name 取值不同的 agent.yaml。
// 资源本身仍然合法（APM 不比较二者），仅用于校验 validate 输出的 warning（ADR-034）。
const lifecycleAgentYAMLMismatchedID = "schema_version: 1\nagent:\n  id: bibit-internal-demo\n  name: demo-agent\n  version: v1.0.0\n  description: demo\n  soul: help safely\n"

// writeLifecycleAgentYAML 在 dir 下写入给定 agent.yaml 文本，返回资源根目录路径。
func writeLifecycleAgentYAML(t *testing.T, dir, agentYAML string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(agentYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeLifecycleAgent 在 dir 下写入合法 V0.5 目录型 Agent 资源根（agent.yaml），
// 并返回该资源根目录路径，供 Submit/Validate 按目录语义消费。
func writeLifecycleAgent(t *testing.T, dir string) string {
	t.Helper()
	return writeLifecycleAgentYAML(t, dir, lifecycleAgentYAML)
}
