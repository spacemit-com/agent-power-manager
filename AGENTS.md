# Agent Instructions

- 完成重大决策后，引导用户在 ADR 中创建记录，并在相关文档中引用对应的 ADR ID。
- Superpowers brainstorming 生成的 spec 和 plan 应保存到对应的 feature 文件夹。
- 实现重要接口时，应添加简要、规范的中文注释，说明接口用途以及必要的约束。

## 安装/卸载功能测试

- 当用户要求执行 APM 安装/卸载功能测试时，先读取并遵循 [测试指引](docs/testing/install-uninstall.md)，按其中的固定云端 Power、目标 agent 选择、CLI 预检、目录隔离和清理流程执行。
- 按 [报告模板](docs/testing/install-uninstall-report-template.md) 输出实际证据；未执行或阻塞的项目不得记为通过。仅要求编写或修改测试文档时，不运行真实安装/卸载。
