# 构建指南

本指南说明如何在本地构建、验证和运行 APM 命令行工具。

## 前置条件

- Git
- Go 1.26.5（版本以 [`go.mod`](go.mod) 为准）

在仓库根目录确认 Go 版本：

```bash
go version
```

## 快速构建

在仓库根目录执行：

```bash
go mod download
go build -o ./apm ./cmd/apm
./apm --help
```

构建完成后，可执行文件位于 `./apm`。

## 开发验证

提交前建议依次执行以下检查：

```bash
# 检查 Go 源码是否已格式化；无输出表示通过。
test -z "$(gofmt -l cmd internal)"

# 运行全部单元测试。
go test ./...

# 执行 Go 静态检查。
go vet ./...

# 重新构建命令行程序。
go build -o ./apm ./cmd/apm
```

如需自动格式化源代码，执行：

```bash
gofmt -w cmd internal
```

## 本地运行

构建后可通过下列命令验证命令行参数解析：

```bash
./apm --help
./apm search example --agent all --type skill
./apm list --agent all --installed
./apm install example=1.0.0 --agent all
./apm uninstall example --agent all --type plugin
./apm --verbose search example --agent all
```

全局选项 `--verbose` 输出解析诊断到标准错误；帮助信息可查看每个子命令支持的参数。

当前实现仅完成命令解析和参数校验，`search`、`list`、`install`、`uninstall` 尚未执行业务生命周期操作。

## 清理本地构建产物

删除由本指南生成的可执行文件：

```bash
rm -f ./apm
```
