# 本地任务提交幂等扩展提案

状态：**设计依据；已在独立候选分支实施，按整合后测试验收**。现有普通提交行为保持兼容，HTTP/CLI和MCP的可选扩展分别整合；运行中的二进制不一定包含这些新代码。使用方式与实现边界见[本地运行指南](local-runtime.md)。不增加持久日志，不操作硬件，不改已有 PR或自行发布。

## 1. 无键提交基线与引入条件

当前 `Engine.Submit` 每次成功调用都会创建新 job；HTTP `POST /v1/devices/{serial}/jobs`、CLI `set/run`、MCP `set_device_state/run_program` 均沿用此行为。CLI 和 MCP 不自动重试。MCP 在 POST 传输失败、响应损坏或服务端 5xx 时标记 `outcome_unknown`：请求可能已经执行，超时不等于取消。

因此，丢失提交响应后重发相同 JSON 可能重复播放序列；默认 `replace` 还可能取消原任务或后来提交的任务。重复设色也会重新开始淡入，不能据此认为提交可安全重试。已有 job ID 时可以查询、取消；没有 ID 且未保存回执绑定时，无法精确关联那次无键提交。

交互式本地操作仍可使用当前接口。需要应对 HTTP/MCP 响应丢失、Agent 恢复会话，或将来接入会重复投递的队列时，才启用本文的**显式可选**扩展。当前接口不强制先获取 token，也不添加默认自动重试。

## 2. 保证与边界

对同一 daemon 实例、同一 `(serial, request_id)`：有效回执保留期内，规范化后的 program 和 policy 相同，返回同一个 job 的最新状态；不同则返回 `409 idempotency_conflict`。回执过期后只能拒绝旧键，不能重新执行。第一次接受、去重记录、队列变更必须原子完成，并发请求只能创建一个 job。

保证的是“一个请求键最多准入一个逻辑任务”。它不保证任务成功，不保证每条 USB 指令只发送一次，也不把 accepted、SDK 读回或 succeeded 当作摄像头确认。失败或取消的 job 仍返回原 job，不自动重新执行。设备断线重连保留实例及回执；新建 Engine、进程重启或配置重建产生新实例。

不引入持久日志时，重启会丢失任务和回执，无法承诺跨重启 exactly-once。客户端不得把旧请求自动换绑到新实例。云端离线投递、长时间重试和持久任务恢复另行设计，本文不实现 MQLite 等适配器。

## 3. 可选协议

仅增加两个提交字段，必须同时省略或同时提供非空字符串；显式 null、未知字段、重复 JSON 键仍拒绝。

```json
{
  "program": {"steps": [{"color": "#102030"}], "on_finish": "hold"},
  "policy": "replace",
  "request_id": "req_120337_0123456789abcdef0123456789abcdef",
  "expected_daemon_instance_id": "daemon_0123456789abcdef0123456789abcdef"
}
```

纯 UUID 加 TTL 有一个漏洞：忘记旧键后，迟到的同键重试会被当作新任务。建议在**这项可选功能内部**采用有截止时间的 token，以有限内存拒绝过期旧请求，而不维护无限墓碑：

- Engine 创建时用 `crypto/rand` 生成 128 位实例 ID；随机源失败则初始化失败。实例 ID 与 PID、设备序列号无关。
- 新增 `GET /v1/instance`，返回 `daemon_instance_id`、`idempotency.supported`、`request_id_prefix` 和以下限额。旧 `/healthz` 响应保持不变。
- prefix 形如 `req_120337_`，数字是 **daemon 单调运行时间的毫秒截止点**，由服务端选为当前运行时间加 60 秒；客户端追加 128 位随机小写十六进制 nonce。无需客户端时钟，也不需要服务端预留键。
- 首次准入须满足 `now < deadline <= now + 60000ms`；严格校验格式、数字长度及溢出。已有回执的重放先查回执，不受准入截止点限制。服务端使用保留单调分量的时间差，墙钟调整不能让旧 token 重新有效。
- 实例不符返回 `409 daemon_instance_changed`；缓存未命中且 token 已过期返回 `410 request_expired`，绝不按新任务执行。重新获取 token 表示一个**新的用户意图**，不能用它自动补发旧意图。

普通提交继续只发送 program/policy。旧服务端不支持新字段时应明确失败；客户端不能去掉字段后悄悄降级提交。

## 4. 查询与响应丢失后的处理

新增只读路由：

```text
GET /v1/devices/{serial}/requests/{request_id}?expected_daemon_instance_id=daemon_…
```

| 结果 | 含义及客户端动作 |
| --- | --- |
| `200`，原 Job | 已找到接受记录，按其实际状态查询；不新建任务。 |
| `404 request_not_found`，token 尚有效 | 仅说明查询当时未找到。原 POST 可能仍在途中；允许调用方显式重发**相同实例、键、payload**，不得换键。 |
| `410 request_expired` | 已无可查询回执，拒绝新准入；旧请求是否曾执行仍可能未知。 |
| `409 daemon_instance_changed` | 实例已换，旧结果未知；不自动绑定新实例。 |

首次提交与命中重放均返回 `202` 和现有 Job JSON，即使原 job 已终结；保留现有客户端按 202 判断 accepted 的行为。可增加 `X-NB1-Replayed`、`X-NB1-Daemon-Instance-ID` 响应头，不给 Job 增加必填字段。有键提交的 `Location` 指向回执查询，无键提交仍指向 `/v1/jobs/{id}`。

HTTP 错误沿用当前 `{error:{code,message}}`。MCP 对过期或实例变化的旧意图显示 `outcome_unknown:true`，不能因为它们属于 4xx 就宣称旧操作未发生。收到服务端明确的首次拒绝，可以判断这次未准入；丢失响应时仍须保留请求二元组和未知结果提示。

## 5. Engine 内的最小实现

保留 `control.Service.Submit` 和现有 mock；增加一个可选能力接口提供 `Instance`、带 metadata 的提交和回执查询。HTTP 仅对显式扩展请求使用该能力，未实现者明确报不支持。旧 Submit 走无键路径，新路径与它共用队列准入实现。

规范化继续使用当前 `Normalize`：repeat 省略或 0 均为 1，on_finish 省略为 off，颜色转小写；policy 省略为 replace。对有版本号的固定字段结构序列化后计算 SHA-256，包含 serial、规范化 program、规范化 policy；JSON 键序与 RGB 大小写不影响匹配，policy 变化属于冲突。

持有同一 `Engine.mu`，按以下顺序处理；不在锁内做 USB I/O：

1. 在锁外校验、规范化输入；锁内先核对实例，再查 `(serial, request_id)`。
2. 命中且摘要相同，返回原 record 的快照；摘要不同返回 409。命中不得因设备离线、队列满或 Engine 已进入关闭状态而创建新任务。
3. 未命中则检查截止点，清理可过期回执，检查回执容量、Engine/设备状态与 policy 准入条件，生成新 job ID。所有拒绝及随机源失败必须发生在 `replace` 取消旧工作之前；回执命中不依赖随机源。
4. 在同一临界区内发布回执与 job、完成队列变更并唤醒 worker。任何失败都不得留下半条回执或取消旧任务。取消的旧队列项也照常保留各自回执。

## 6. 回执限额与任务历史分离

建议首版固定默认值，随 `/v1/instance` 公开；先不增加一组用户配置开关：

| 限额 | 建议值与含义 |
| --- | --- |
| 新 token 准入窗口 | 60 秒；适合当前本地调用，非离线消息时限。 |
| 终态回执保留时间 | 至少终结后 60 秒，且不早于 token 截止点；查询、重放不续期。 |
| 回执数量 | 全 Engine 4096 条，包含排队、运行及终态记录。 |
| 回执引用的 program 步数 | 合计 65536 步，按 steps 长度计算，不展开 repeat。 |

排队、运行、cancelling 的回执不因 TTL 被删除；到终态才计算保留截止点。**不驱逐任何未过期记录**；额度耗尽对新键返回 `429 idempotency_capacity`，旧键仍可查询、重放。终态清理在新准入/查询时执行即可，最多扫描 4096 项；无须缓存框架或额外守护 goroutine。

现有终态历史默认 128 条，继续单独淘汰。回执持有同一个 record 引用，历史索引删除不能破坏有效回执；因此普通 `/v1/jobs/{id}` 可能 404，回执路由仍能返回 Job。引用计数/额度只随回执插入删除变化，不因重放重复计费。

短任务持续使用新键的理论回执容量约为 `4096 / 60 ≈ 68 次/秒`，还须扣除活动任务占用；这不是 USB 吞吐承诺。1024 步的大程序会更早耗尽步数预算。步数组约为数 MiB 量级，另有 record、字符串及索引；限额约束去重保留，不是整个进程 RSS 上限。长期阻塞的 native I/O 可以一直占用活动槽位，此时拒绝新键，不通过遗忘旧键恢复容量。

## 7. CLI 与六个 MCP 工具的兼容扩展

- CLI 增加 daemon-only 的 `control prepare`，输出完整二元组；`set/run` 增加可选 `--request-id`、`--expected-daemon-instance-id`；`control request SERIAL REQUEST_ID --expected-daemon-instance-id ID` 查询回执。直接模式遇到这些选项，在取锁/打开 USB 前报错。原有命令不增加准备步骤。
- MCP 保持六个工具名称。`list_devices` 可选 `prepare_request:true` 时额外读取 instance，并在输出外层增加 `request_ticket`；原 `{}` 行为与设备 Data 数组不变。准备不分配回执、不操作 USB。
- `set_device_state/run_program` 增加同一对可选字段；`get_job` 接受原 `{id}`，或互斥的 `{serial,request_id,expected_daemon_instance_id}` 查询方式。`get_device_state/cancel_job` 不变。
- dry_run 仍只在本地规范化、不发送 HTTP，明确实例绑定尚未验证。set/run 工具的全局 `IdempotentHint` 仍为 false，因为无键调用仍可重复提交。
- CLI/MCP 默认不自动重试，不自动生成新 token 补发。已知键的调用方可主动查询或重发相同请求。客户端保存 token 后再发 POST；准备 token 不能证明任务已接受。

## 8. 实施顺序与验收

第一阶段已是现状：诚实报告未知结果，保留原接口。第二阶段已在独立候选分支实施上述实例、回执和显式字段，内存模型、HTTP、CLI/MCP 并行整合后统一验收；OpenAPI、中文指南和示例随实现更新。第三阶段才评估持久日志、长周期重投与云队列，不把第二阶段包装成持久任务系统。

必须通过的测试（fake clock、fake backend 与屏障同步，无需 USB）：

1. 100 个并发相同键、规范化等价 JSON，只产生一个 job/一次程序执行；replace、queue、reject 的副作用只发生一次。不同颜色、LED、时序、on_finish 或 policy 返回冲突，无额外副作用。
2. 命中原记录时覆盖 queued/running/cancelling/各终态，以及后来离线、队列满、回执满、关闭状态；failed/cancelled 不重新执行。
3. 数量/步数额度耗尽先返回 429，不能取消原 replace 目标；已有键仍命中；失败准入不占槽。普通历史淘汰后，有效回执仍可查询。
4. 活动任务超过准入时限仍保留；终态保留从完成时起算，查询和重放不续期。过期清理后原 token 始终 410；覆盖溢出、非法格式、过远截止点和墙钟跳变。
5. 新 Engine/同端口重启返回新实例，旧绑定 409，不触发硬件执行或自动换绑。
6. HTTP 在准入后故意丢失响应，查询/同键重发只找到一个任务；准入前中断与查询未命中的竞态也不能重复创建。覆盖严格 JSON、状态码、202 终态重放与旧接口回归。
7. 官方 MCP stdio 测试覆盖六工具原参数兼容、可选准备/查询、未知结果保留 token、dry_run 零 HTTP；CLI 直接模式拒绝扩展不取锁。Go 1.18 核心、独立 Go 1.25 MCP 构建与 race 测试通过，无新增依赖。
