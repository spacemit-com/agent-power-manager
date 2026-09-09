package submission

import (
	"errors"

	"apm/internal/domain"
)

// ErrInvalid 表示提交内容未通过本地校验；具体原因包装在错误链中。
var ErrInvalid = errors.New("invalid submission content")

// Request 描述一次 validate/submit 的输入。
type Request struct {
	Type    domain.PowerType // skill | plugin | mcp | agent
	Path    string           // skill/plugin 源目录、mcp JSON 文件，或 agent 源目录（agent.yaml + 可选 assets/）
	Message string           // submit 必填；仅描述本次提交，不参与资源身份校验
}

// Validation 描述 validate 的输出（身份、统计与警告，不含打包字节）。
type Validation struct {
	Type        domain.PowerType
	Name        string
	Version     string
	Description string
	FileCount   int
	ContentSize int64
	Warnings    []string
}

// Content 描述 submit 确定性打包或规范化后的内容与身份。
type Content struct {
	Type        domain.PowerType
	Name        string
	Version     string
	Description string
	Message     string
	Definition  *domain.MCPServer // 仅 MCP 提交携带
	ContentType string
	Bytes       []byte
	MD5         string
}
