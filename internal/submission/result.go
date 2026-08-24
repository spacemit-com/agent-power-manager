package submission

import (
	"errors"

	"apm/internal/domain"
)

// ErrInvalid 表示提交内容未通过本地校验；具体原因包装在错误链中。
var ErrInvalid = errors.New("invalid submission content")

// Request 描述一次 validate/submit 的输入。
type Request struct {
	Type          domain.PowerType // skill | plugin | mcp | agent
	Path          string           // skill/plugin 源目录、mcp JSON 文件，或 agent 的单个 agent.yaml 文件
	Version       string           // 仅 skill 必填；plugin/mcp 从源读取；agent 禁止 --version（版本来自 agent.yaml）
	ChangeSummary string           // skill/plugin/agent 必填；mcp 从源读取
}

// Validation 描述 validate 的输出（身份、统计与警告，不含打包字节）。
type Validation struct {
	Type          domain.PowerType
	Name          string
	Version       string
	Description   string
	ChangeSummary string
	FileCount     int
	ContentSize   int64
	Warnings      []string
}

// Content 描述 submit 确定性打包或规范化后的内容与身份。
type Content struct {
	Type          domain.PowerType
	Name          string
	Version       string
	Description   string
	ChangeSummary string
	ContentType   string
	Bytes         []byte
	MD5           string
}
