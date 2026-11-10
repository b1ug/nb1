# 中文运行时默认文本待办

范围：固定基线与当前文档片内所有非`*_test.go` Go源的字符串字面量。使用Go AST解析并解码字符串，仅盘点含Han字符的字面量，注释不计。`docs/contract-fixtures/generate.go`中的中文Pattern标题属于用户/fixture数据，明确排除，不能当作默认产品文案翻译；JSON例子与hash绑定资料也不改。

完整结果是 [runtime-chinese-inventory.json](runtime-chinese-inventory.json)：**197个字面量/11个生产文件**。每项包含path、line、category和完整原字符串；一个多行Long说明仍计一个字面量。工具源保留为 [runtime-inventory.go.txt](runtime-inventory.go.txt)，可复制到临时`.go`文件后用`GOTOOLCHAIN=local GOPROXY=off go run`重现，避免作为生产包加入根模块。

| 路径 | 字面量数 | 待办 |
| --- | --- | --- |
| `cmd/root.go` | 2 | 根命令Short/Long默认说明 |
| `cmd/control_color.go` | 3 | 颜色命令帮助/输入校验错误 |
| `cmd/control_preview.go` | 4 | capabilities/preview默认帮助 |
| `cmd/doctor.go` | 6 | doctor帮助、flags、attention错误 |
| `internal/doctor/doctor.go` | 43 | 校验错误、诊断边界、建议、默认文本输出 |
| `cmd/service.go` | 40 | 用户服务命令帮助/flags、平台限制、状态输出 |
| `internal/service/user.go` | 43 | macOS用户服务归属/状态/操作错误 |
| `internal/service/systemduser/files.go` | 15 | Linux托管文件/启用链接校验错误 |
| `internal/service/systemduser/manager.go` | 32 | Linux用户manager查询/操作/归属错误 |
| `internal/service/systemduser/properties.go` | 8 | Linux用户manager属性解析错误 |
| `internal/service/systemduser/types.go` | 1 | Linux服务配置错误类型文字 |

分类汇总：CLI帮助/flags/输出/错误15；doctor诊断与默认建议43；用户服务帮助/状态/错误139。生产MCP对象没有因本片改动；本次盘点也未发现MCP生产Go字面量中的中文默认文字。

后续最小运行时片应分别处理CLI/doctor与macOS/Linux服务文本及与它们直接耦合的测试断言。保持机器字段、code、枚举、默认值和权限/归属/unknown边界不变；不能为了翻译改成更宽松的服务操作。若未来要翻译MCP工具description/schema，应使用独立版本和独立兼容审查，不能回写冻结的旧16工具对象。

其余本地化/历史文档仍可中文，例如`README.zh-CN.md`、`docs/api-reference.zh-CN.md`、`docs/agent-guide.zh-CN.md`、内部README与规划站。无后缀的既有中文专题页（如`docs/patterns-cli.md`、`docs/local-runtime.md`）在公开入口中已明确标为localized，后续可另加英文伴随页；本片新英文手册已完整覆盖本次指定的API面。是否扩大其余指南本地化应另成文档片，不混入运行时翻译。

设备RAM/Flash/notes能力继续按官方来源研究和最小提案分开推进，现有主机Pattern接口并不证明它们已实现。发布、安装、宿主注册、OS服务、硬件/摄像验收均不属于此盘点动作。
