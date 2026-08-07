# Agent Power Manager

APM 领域词汇表，用于统一描述 Agent 与 Power 的发现、安装和配置治理。

## Language

**Agent Power 安装清单**：
APM 为单个 Agent 维护的已安装 Power 引用集合；其中每个 Power 引用的版本均已确定。
_Avoid_：Agent 能力清单、能力清单

**Power 引用**：
由 Power 类型、名称和可选版本组成的标识；写入 Agent Power 安装清单时版本不可为空。
_Avoid_：InstalledPower、安装记录

**Power 分组**：
按 Power 类型组织的 Power 引用集合，用于表达单个 Agent 已安装的全部 Power。
_Avoid_：InstalledPower 列表、扁平 Power 列表
