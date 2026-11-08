# blink(1) 本地 MCP 适配器

这是独立的 stdio 服务：Agent → MCP → 本机 HTTP daemon → 每设备执行器 → SDK/GID → USB。MCP 进程不打开 USB，不会抢占设备，也不会在 HTTP 失败时改为直控；多个 Agent 可以连接同一个 daemon。

采用官方 [Go MCP SDK v1.8.0](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.8.0)，协议协商、stdio framing、输入/输出 schema 验证均由 SDK 处理。该版本的 [go.mod](https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/go.mod)要求 Go1.25；因此适配器单独建模块，不改变 GID/设备 SDK 的 Go 下限。支持的协议版本由 SDK 协商，当前最新为 [2026-07-28](https://github.com/modelcontextprotocol/go-sdk#version-compatibility)。

## 启动

先启动设备 daemon；显式选择设备序列号：

```bash
# 工作区根目录
./bin/nb1-runtime server --devices 20005601

# 另一个终端，构建适配器
cd nb1/adapters/mcp
GOWORK=off go build -o ../../../bin/nb1-mcp .
```

`nb1-mcp --endpoint http://127.0.0.1:8931` 使用 stdin/stdout 交换 MCP 消息。默认每次 HTTP 请求的期限是10秒，可通过 `--timeout 5s` 修改。只接受 loopback HTTP origin；不会跟随重定向、使用代理环境变量或启动第二个硬件 daemon。stdout 专用于协议，错误写入 stderr。

可在支持 stdio MCP 的客户端中参考 [mcp-config.example.json](mcp-config.example.json)，将 `command` 改成二进制的实际绝对路径。文件只是示例，本轮没有修改任何客户端配置或自动注册工具。

## 工具

| 工具 | 输入与结果 |
| --- | --- |
| `list_devices` | 原 `{}` 返回设备数组。可选 `prepare_request:true` 额外读取实例信息，在输出外层加入 `request_ticket`；`data` 仍是设备数组。准备不会创建任务或预留回执。 |
| `get_device_state` | `serial`。返回逻辑目标和带时间戳的原始 RGB 观测快照，保留其采样时间。 |
| `set_device_state` | `serial`、`color`，可选 `led`、`fade_ms`、`hold_ms`、`policy`、`dry_run` 及齐全的请求二元组。编译成完成后保留颜色的单步程序，返回 job。 |
| `run_program` | `serial`、`program`，可选 `policy`、`dry_run` 及齐全的请求二元组。提交严格结构化程序。 |
| `get_job` | 原 `{id}`，或互斥的 `{serial,request_id,expected_daemon_instance_id}`。后者只查询原回执，不提交任务；任务历史与回执均有界且不跨 daemon 重启保存。 |
| `cancel_job` | `id`。请求取消；可能返回 `cancelling`，此时还不能认为硬件已停止。 |

输入仅接受声明的字段。`color` 为原始 `#RRGGBB`，不隐式应用 Gamma；`led` 为0（全部）、1或2。`fade_ms` 在0–600000之间且必须是10的倍数；`hold_ms` 在0–3600000之间。程序有1–1024步，repeat为0或省略时运行一次，最大10000次；每步的淡入、保持及10ms固件更新余量计入24小时名义上限，原生 I/O 仍有额外开销。`on_finish` 默认为 `off`，也可指定 `hold`。能力与实际支持型号以 daemon 结果为准，不据协议模拟声称 mk3 已经实测。

接收中间件在 SDK 类型转换前检查原始工具 `arguments`：拒绝重复键（包括转义后同名的键）、任何显式 `null`、超过64层或1 MiB的对象。省略 `arguments` 按空对象处理，之后仍须满足对应工具的必填字段；非规范大小写和未知字段由 SDK 的严格 schema 拦截。这样不会把先给出的设备/LED选择静默替换成后一个值。

```json
{
  "serial": "20005601",
  "dry_run": true,
  "policy": "replace",
  "program": {
    "steps": [
      {"led": 1, "color": "#ff0000", "fade_ms": 300, "hold_ms": 500},
      {"led": 2, "color": "#0000ff", "fade_ms": 300, "hold_ms": 500}
    ],
    "repeat": 2,
    "on_finish": "off"
  }
}
```

`dry_run` 只做本地验证并返回规范化程序，完全不访问 HTTP 或 USB。

## 可选的请求回执

需要关联可能丢失响应的那一次提交时，先调用 `list_devices`：

```json
{"prepare_request": true}
```

它先读取设备，再通过 `GET /v1/instance` 取得实例和准入前缀；适配器只追加由 `crypto/rand` 生成的16字节 nonce。输出保持 `kind:"devices"` 和 `data` 数组，额外包含：

```json
{
  "request_ticket": {
    "request_id": "req_60000_0123456789abcdef0123456789abcdef",
    "expected_daemon_instance_id": "daemon_0123456789abcdef0123456789abcdef"
  }
}
```

示例中的键仅说明格式，真实调用必须使用准备所得的完整二元组。保存它后，再把两个字段同时加入 `set_device_state` 或 `run_program`；省略两字段仍走原提交路径。默认不准备 token、不重试写入、不更换实例或请求键，也不会在旧 daemon 不支持扩展时去掉字段降级。两种提交工具的 `IdempotentHint` 继续为 false，因为无键调用可以创建新任务。

同一实例、设备、键和规范化 payload 在服务端有效回执期内映射至同一个逻辑 job；这不承诺 USB 指令恰好发送一次、任务必定成功或跨重启恢复。重放仍可能返回 HTTP202 和已经终结的原 job，应读取 `data.status`。二元组在提交结果和 HTTP/传输错误中以外层 `request_ticket` 原样保留。

查找原提交时调用：

```json
{
  "serial": "20005601",
  "request_id": "req_60000_0123456789abcdef0123456789abcdef",
  "expected_daemon_instance_id": "daemon_0123456789abcdef0123456789abcdef"
}
```

以上是 `get_job` 的另一种完整输入，不能同时提供 `id`。`request_expired`（410）、`daemon_instance_changed`（409）及回执查询的 `request_not_found`（404）保留 `outcome_unknown:true`：404只表示查询当时没有回执，原 POST 可能仍在途中。不要把旧意图自动换成新键或绑定到新实例；已知键的调用方可以显式查询，或按服务端规则主动重发相同实例、键和 payload。

有二元组的 `dry_run` 仍然零 HTTP，只检查字段格式及程序边界，预览保留原二元组并明确返回 `instance_binding_verified:false`。它不能验证 daemon 实例是否匹配、回执是否存在、准入期限是否过期；期限是 daemon 单调运行时间，不能与客户端墙钟比较。

该可选扩展已实现并覆盖模拟 HTTP 与官方 SDK stdio 软件测试；此分支未执行新的硬件回执验收，实物结果由主工作区集成后单独记录。

## 任务与错误语义

返回值同时包含 `structuredContent` 和 JSON 文本兼容内容。`kind` 区分设备列表、状态、job、预览和错误，`data` 保留 daemon 的原始结构。

- `accepted=true` 仅表示看到了 HTTP202，并不表示执行完成。`data.status` 是实际任务状态；非终态结果提供 `poll_after_ms=200` 作为建议。
- `queued`、`running`、`cancelling` 需要继续查询；`succeeded`、`cancelled`、`failed` 是终态。取消后要等到 `cancelled` 才表示执行器确认硬件清理；清理失败会保留 `failed`。
- HTTP 错误保留 `error.code`、`message`、`http_status` 并设置 MCP `isError=true`。输入 schema 错误由官方 SDK 返回 `isError`。
- POST 超时或响应损坏时，可能已被 daemon 接收：`error.outcome_unknown=true`，不会盲目重试。此时 `accepted=false` 只表示没有确认到202，不能解释为未执行。应查询设备/任务，必要时显式停止已有任务。
- MCP 请求超时或 MCP 会话关闭，不会替用户取消 daemon 中已接受的任务。请求取消应调用 `cancel_job`。

本适配器不提供固件升级、Flash 写入、任意 HID 报文、系统服务安装或公网控制。

## 验证

```bash
GOWORK=off go test -race -count=1 -timeout=45s ./...
GOWORK=off go vet ./...
```

测试用模拟 HTTP 服务，并用官方 SDK 客户端覆盖工具发现、原始参数歧义与 schema 拦截、程序预览、真实 stdio 子进程通信、队列错误、取消中状态、POST 超时歧义及重定向/响应上限。重复键、嵌套 null、大小写别名和输入限制用例确认在任何 HTTP 请求前被拒绝。不会打开真实 USB。硬件链路应由主工作区另行验收。

回执测试同样使用官方 stdio 客户端，覆盖六工具旧输入、准备键的随机性、成对字段与查询互斥、原 payload/键保留、终态202、过期/实例变化/404未知结果、POST丢失响应不重试，以及带键 dry-run 零 HTTP。没有引入额外 Go 依赖或提高模块 Go1.25 下限。

另有默认跳过的真实链路验收：显式提供 endpoint 和 serial 时只查询；额外提供 `NB1_MCP_LIVE_COLOR` 才会发送设色任务。它通过官方 MCP 客户端启动实际适配器二进制，整个过程中仍由 daemon 持有 USB。

```bash
NB1_MCP_LIVE_ENDPOINT=http://127.0.0.1:8931 \
NB1_MCP_LIVE_SERIAL=20005601 \
GOWORK=off go test -run '^TestLiveDaemonOptIn$' -v
```

需要实际设色时，可在同一命令中额外设置 `NB1_MCP_LIVE_COLOR='#000000'`。成功设色会保留该颜色；其他测试脚本负责安排后续操作。`NB1_MCP_LIVE_BINARY` 可覆盖默认 `.build/nb1-mcp` 路径。

另有默认跳过的 `TestLiveReceiptsOptIn`：必须额外设置 `NB1_MCP_LIVE_RECEIPTS=1` 并显式提供 `NB1_MCP_LIVE_COLOR` 才会提交任务。它准备二元组、同键提交两次并查询原 job、验证换 payload 的409且颜色不变，最后提交熄灯并确认；此测试不会默认亮灯。先构建待验收二进制，再显式选择该测试：

```bash
NB1_MCP_LIVE_RECEIPTS=1 \
NB1_MCP_LIVE_ENDPOINT=http://127.0.0.1:8931 \
NB1_MCP_LIVE_SERIAL=20005601 \
NB1_MCP_LIVE_COLOR='#000000' \
GOWORK=off go test -run '^TestLiveReceiptsOptIn$' -v
```

接口依据为工作区 `nb1/internal/control/types.go` 与 `internal/httpapi`。适配器当前保留独立 wire 类型和相同校验，避免导入 HID 模块；今后接口演进需要共享 schema/契约测试，不应单边修改。
