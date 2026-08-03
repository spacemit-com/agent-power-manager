// 由 workspace.dsl 的 !include 引用；本文件内容处于 Web Application 容器内。
// 组件应拥有清晰职责，可由代码模块、类或服务内子系统实现。

api = component "API" "接收请求并协调用例。" "Technology"
service = component "Application Service" "承载业务用例。" "Technology"
repository = component "Repository" "封装数据访问。" "Technology"

api -> service "Invokes"
service -> repository "Uses"
repository -> system.database "Reads from and writes to"
