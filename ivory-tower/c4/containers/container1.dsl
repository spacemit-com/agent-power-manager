// 由 workspace.dsl 的 !include 引用；用于按职责拆分容器定义。
// 容器表达可独立部署或运行的应用、服务、数据库和队列，不是代码目录。

worker = container "Worker" "异步处理后台任务。" "Technology"

webApplication -> worker "Submits work"
worker -> database "Reads from and writes to"
