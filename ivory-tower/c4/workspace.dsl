workspace "System Architecture" "使用 C4 描述系统结构。" {
    !identifiers hierarchical

    model {
        user = person "User" "使用系统的目标用户。"
        system = softwareSystem "System" "待设计的软件系统。" {
            database = container "Database" "持久化业务数据。" "Technology"
            webApplication = container "Web Application" "提供用户界面和 HTTP 接口。" "Technology" {
                !include components/component1.dsl
            }

            !include containers/container1.dsl
        }

        user -> system "Uses"
    }

    views {
        systemContext system "SystemContext" "系统上下文。" {
            include *
            autoLayout lr
        }

        container system "Containers" "容器视图。" {
            include *
            autoLayout lr
        }

        component system.webApplication "Components" "组件视图。" {
            include *
            autoLayout lr
        }

        theme default
    }
}
