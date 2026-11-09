# blink(1) Agent 使用指南

此文对应已实现的 Program/Effect v1 与七个 MCP 工具。它提供使用方法；设备与软件能力以运行时结果为准。项目内 [skill](../skills/blink1-control/SKILL.md) 不会自动安装或被所有 Agent 自动读取。

## 入口和抽象

真正的 stdio MCP 服务使用官方 Go SDK 处理 JSON-RPC，再通过本地 HTTP 控制 daemon，没有 shell/CLI 转发。CLI 的默认 daemon 模式也访问同一个 HTTP 服务。两者共享设备所有者、编译器、Job 和取消语义；不会因为 HTTP 失败而转直控。

| 层次 | 用途 | 当前输入 |
| --- | --- | --- |
| Program | 精确步骤 | steps：led、color、fade_ms、hold_ms；repeat、on_finish |
| Effect | 简短意图 | steady / pulse，编译成同一个 Program |
| Pattern | 命名与复用 | 规划中，不能调用尚不存在的工具 |

当前 color 必须六位 `#RRGGBB`，接受大小写并规范为小写，值是原始 RGB，不隐式 Gamma。不要把旧 SDK 的名字/#rgb/rgb()/hsb() 支持误当成新接口能力。已知的客户端转换必须明确值；例如鲜绿可以选 `#00ff00`。名字表不能混用：旧 SDK green/lime 映射与 CSS 同名值相反。 迁移旧配置时还需核实原入口是否应用 Gamma/亮度：保留输入值、保留实际发送值和保留视觉效果是不同目标。先说明选择，不能重复补偿或承诺未校准硬件的视觉等价。

## 从预览到执行

先 `list_devices({"include_capabilities":true})`，选择实际 serial。旧 daemon 若不支持能力目录，可另用 `list_devices({})` 只读查询设备，仍将目录标记为 unsupported。下面示例 serial 只是占位，不能照抄作为自动选择。

只想查看效果，用 `preview_program`：

```json
{"effect":{"kind":"pulse","color":"#00a000","cycles":2,"period_ms":800,"on_finish":"off"}}
```

preview 使用 POST 但不提交 Job，失败不会产生未知 Job 结果。这是纯软件权威编译；可选 serial 只检查缓存能力。当前契约的此例得到三个步骤、repeat=2、总步骤6、名义1600ms；不是实灯或硬实时证明。响应 accepted=false 是预览未提交的正常结果。旧 `dry_run` 对 Effect 仅检查结构，不能替代此预览。

明确要求执行后，`run_program` 参数示例：

```json
{"serial":"REPLACE_WITH_SELECTED_SERIAL","effect":{"kind":"pulse","color":"#00a000","cycles":2,"period_ms":800,"on_finish":"off"},"policy":"reject"}
```

保持颜色可用 `set_device_state` 或 steady。实际提交前沿用已有授权，不要求每次都重新批准。示例显式 reject 避免打断别人的任务；API 兼容默认是 replace。需要打断或排队时显式选择相应策略。

得到 id 后查 `get_job({"id":"实际Job id"})`。queued/running/cancelling 仍需观察；succeeded/cancelled/failed 为终态。取消用 `cancel_job` 并查到终态；若已经 cancelling，继续查询原 Job 即可。观察预算到期时说明尚未确认清理完成，不改为杀服务或补发 off。等待超时或 MCP 会话结束不会撤销已接受任务。

## 不确定结果与兼容

需要可关联提交时，可在执行前调用 `list_devices({"prepare_request":true})`，保存返回的完整 request_ticket；其二元组一起加入提交，预览无需票据。提交响应丢失后，用同一 serial/request_id/expected_daemon_instance_id 调 `get_job`。该查询的三个字段必须来自原记录，不能同时带 id；没有保存值时先找原记录，不随机补键。过期、实例变更、404 或查询失败均不能证明旧提交从未执行。不自动换键、补发或去票据降级。

能力目录描述远端软件；本客户端 schema 决定它能调用什么。v1 的 preview/catalog 当前拒绝未知响应字段，因此新增字段也可能需要新客户端。404/501 表示该 daemon 不支持路由，不能隐式直控或伪造预览。详见 [MCP 协议、错误与票据](../adapters/mcp/README.md)、[HTTP/OpenAPI](openapi.json)、[完整 CLI/API 用法](local-runtime.md)。

## 如何给出可信结果

分别说明：编译通过、Job 是否完成、设备 RGB 读回与采样时间、是否有相机视觉证据。动态 pulse 的状态采样不一定捕获全部过渡；succeeded 也不是光学校准。设备 currently supported 不代表 mk3 已实测；当前真机证据是选定 mk2。

本地 CLI 只读入口为 `control capabilities`、`control preview --program FILE` / `--effect FILE`。文件是裸 Program/Effect 对象，不是 HTTP 信封。显式 direct preview 仅用本地编译器且不能带 serial；旧 `write --preview` / `play --preview` 仍操作硬件。不要借它们完成“只看看”的任务。

命名库、持久记忆、自动重播和硬件 RAM/Flash 是不同能力。本指南不授权安装服务、提权、改隐私授权、任意 HID/Flash 或公网访问；正常使用无需这些额外权限。
