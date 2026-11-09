# blink(1) Agent 使用指南

本指南描述源码中已接通的高级 CLI/MCP 接口，不记录某台机器安装了哪个提交。先核对所用二进制、daemon，以及宿主实际 `tools/list` / schema。MCP 链路为 Agent → 真实 stdio MCP → 本机 HTTP → nb1 执行器 → SDK/GID → USB，不通过 shell 调用 CLI。

[项目 Skill](../skills/blink1-control/SKILL.md)与[llms.txt](../llms.txt)仅是项目内指导，不全局安装、不注册工具，也不增加设备操作权限。

## 选择高级表达

| 表达 | 内容与入口 |
| --- | --- |
| light Action v1 | `nb1-light-action/v1`：set/off/fade/blink/breathe/pulse/alternate/rainbow；MCP preview_light/run_light，CLI control light |
| light Action v2 | `nb1-light-action/experimental-v2`：高级 orbit；同一 MCP 工具及 CLI 命令按明确合同分派 |
| 原 Program / Effect | 精确 steps，或 steady/旧对称 pulse；preview_program/run_program 与原 CLI，不改变旧语义 |
| Pattern | 主机库中的冻结 v1 记录与精确 revision；get_pattern 读取，pattern_ref 预览/执行 |

高级请求仍传 `contract + action`。v2 的 FrameProgram 是 nb1 编译结果，不要求调用者手写底层帧。`frame/sequence/spin/compose` 不是现有 action.kind；也不能把旧 Program 当成新的高级动作。

v1 pulse 的 attack/decay/rest 表达快起、衰减和停顿；旧 Effect pulse 仍用 period_ms 表达对称渐亮渐暗。新的对称呼吸用 breathe。高级动作支持名字、Hex3/Hex6、RGB8、HSV，旧执行字段保持 Hex6；不额外实现色表或 Gamma。

## 实验 Orbit

把下列内容保存为 `orbit.action.json`，供 CLI 读取；该文件没有设备选择或执行票据，单独保存不会亮灯：

```json
{
  "contract": "nb1-light-action/experimental-v2",
  "action": {
    "kind": "orbit",
    "colors": ["red", "blue"],
    "leds": [1, 2],
    "period_ms": 3000,
    "cycles": 3,
    "segments": 4,
    "on_finish": "off"
  }
}
```

4 段是简单长渐变候选，8 段保留更多曲线目标。省略值默认为 4 段、3000ms、1 轮、红/蓝、`[1,2]`、结束 off。初始化只执行一次，同一设备的两路渐变在同一阶段重叠运行；leds 不是设备列表，多设备须分别指定 serial 和原票据。USB 写入仍有间距，不是原子同步。周期、量化与数量上限见[实验契约](../internal/lightactionv2/README.zh-CN.md)。

旧 `nb1-light-action/v1` 的 `alternate.transition:"orbit"` 仍是 66 步/周期的顺序版本；linear/sine/orbit 至少 1300ms、最多 15 轮。它与新 Orbit 不等价，不能把旧文件或已存图案静默换成 v2。软件曲线与录制参数一致也不代表当前设备观感已经通过，须另做实机观测。

## MCP：先发现，再预览和提交

源码保留既有工具，包括 list_devices/get_device_state、resolve_color、preview_light/run_light、preview_program/run_program、get_job/cancel_job、list_patterns/get_pattern 等。工具数量不等于字段支持；只有实际 schema 声明实验 v2 和 job_kind 后才能使用。

需要硬件时，list_devices 选择用户指定或上下文已确定的 serial。`include_capabilities:true` 返回软件目录和设备缓存信息；缓存支持不代表设备空闲或本次会获准执行。只转换颜色直接 resolve_color，不列设备或准备票据。

`preview_light` 接受明确 contract/action 与可选 serial。它仅一次只读编译 POST，返回 `kind:"light_preview"`、`accepted:false`，data 为 `{result,preview}`；不创建 Job。v2 的 `preview.execution` 区分未选设备、不支持、离线或缓存支持。MCP 播放会做目标预览；不能靠修改参数绕过不支持。

以下 `call` 表示宿主一次真实 MCP 调用并返回 structuredContent；selectedSerial 来自本次实际设备选择，不是要填写的示例 ID：

```js
const intent = {
  contract: "nb1-light-action/experimental-v2",
  action: {kind: "orbit", colors: ["red", "blue"], segments: 4,
           period_ms: 3000, cycles: 3, on_finish: "off"}
};
await call("preview_light", {...intent, serial: selectedSerial});
const ticket = (await call("list_devices", {prepare_request: true})).request_ticket;
// 在调用方保存原 serial、intent 和 ticket；下面这一步才会播放。
const accepted = await call("run_light", {
  ...intent, serial: selectedSerial, policy: "reject", ...ticket
});
await call("get_job", {serial: selectedSerial, job_kind: "frame", ...ticket});
```

`run_light` 要求完整原票据，仅接受 reject；先一次目标预览，再至多一次高级执行 POST，不改发低层帧、不自动 prepare、换键、重试或降级。预览与运行的返回程序会绑定；只解析颜色或只预览无需准备票据。

HTTP202 / accepted:true 仅代表接受。V2 返回仍为 `kind:"job"`，但 data 是 FrameJob：使用 completed_frames/total_frames 和 completed_target_writes/planned_target_writes。帧数满可能仍在等待尾部渐变或清理；只有 succeeded/cancelled/failed 是终态。cancelling 还需继续查询。

## MCP：原任务查询与未知结果

已知 ID 时，`get_job({id: accepted.data.id})` 与 `cancel_job({id: ...})` 自动根据严格的 `framejob_` ID 选择帧路由。仅有原票据、没有接受响应或 Job ID 时：

```js
await call("get_job", {serial: selectedSerial, job_kind: "frame", ...ticket});
```

id 与 job_kind/票据查询字段互斥。省略 job_kind，或写 program，仍是旧 Program 回执；适配器不会试探另一个路由。MCP 的后续 get_job 校验回执/ID与帧结构，但不再携带原 Action；调用方保留原预览，用它核对后续完整程序。

超时、断连、坏响应、回执过期或实例变化都不能触发换票重播。保留原 serial、动作、票据和已确认 ID。即使本次预检没有发送新的执行 POST，该票据也可能在先前尝试中使用过；本次失败不能证明此前没亮灯。关闭 MCP 会话或客户端超时不自动取消 daemon Job。

## CLI：daemon、direct 和恢复

以下 `nb1` 必须是已用帮助与版本核实过的二进制。SELECTED_SERIAL 来自当前选定设备，不在文档中固定：

```sh
nb1 control light preview orbit.action.json
nb1 control light preview orbit.action.json --serial "$SELECTED_SERIAL"
nb1 control --mode direct light preview orbit.action.json
nb1 control light run "$SELECTED_SERIAL" orbit.action.json --wait
nb1 control --mode direct light run "$SELECTED_SERIAL" orbit.action.json
```

默认 daemon 模式。direct preview 只做本地编译；direct run 在本进程复用同一个 Engine、设备锁、FrameService 和关闭清理，不通过 HTTP，也不另写一套调度。它内部生成的临时票据不是跨进程 daemon 恢复票据。超时后是否完成清理看实际结果；native 调用未返回时不能宣称所有权已释放或硬件已熄灭。

Daemon run 未提供票据时只 prepare 一次，并在结果中保留它。若要在提交前保存身份，先 `nb1 control prepare`，把真实返回值放入 REQUEST_ID 与 DAEMON_INSTANCE_ID，再执行：

```sh
nb1 control light run "$SELECTED_SERIAL" orbit.action.json \
  --request-id "$REQUEST_ID" \
  --expected-daemon-instance-id "$DAEMON_INSTANCE_ID" --wait
```

失去接受响应后，v2 可以在不知道 Job ID 的情况下恢复查询：

```sh
nb1 control light request "$SELECTED_SERIAL" orbit.action.json \
  --request-id "$REQUEST_ID" \
  --expected-daemon-instance-id "$DAEMON_INSTANCE_ID" --wait
```

已确认 Job ID 时可加 `--job-id "$JOB_ID"`。FILE 必须仍是原 v2 Action；命令只 GET 原回执，不 prepare、播放、取消或改用历史查询。run --wait 与 request --wait 会固定已确认的身份、创建/开始时间，拒绝程序变化、进度回退和状态倒退；错误时保留原票据与最后可信 Job。取消需明确操作已知任务，不能把等待超时当成取消。

需要取消这份 v2 请求时，CLI 同样用原 Action 文件和原票据绑定目标，不必手写 HTTP 请求：

```sh
nb1 control light cancel "$SELECTED_SERIAL" orbit.action.json \
  --request-id "$REQUEST_ID" \
  --expected-daemon-instance-id "$DAEMON_INSTANCE_ID" --wait
```

可选 `--job-id "$JOB_ID"` 进一步绑定已确认的 ID；没有 ID 时先从原回执解析。该命令仅 daemon 模式，先 GET 核对原动作及 Job，再至多一次带原 daemon 实例条件的取消 POST；服务端校验实例后才取消，随后通过原回执确认。`cancel_attempted` 仅表示尝试了取消请求；`already_terminal` 表示此前已终态、没有再发取消 POST。已取消中的任务也只查询。坏响应或超时保留原票据和最后可信 Job，不重试、不换键；`--wait` 后续只查原回执。明确的传输/服务故障可据此恢复，身份、程序或进度错误不会被后续“正常”响应覆盖。`cancel_error` 保留首次取消错误，`cancel_confirmed` 仅表示可信终态为 cancelled；自然完成的 succeeded 不等于取消成功。

取消可能先返回 cancelling；等 cancelled 才代表执行器确认清理，清理失败则是 failed。任务若已成功并 hold，取消不会清除保留颜色；需要熄灯时另发经过授权的 off 动作。原 `control cancel ID` 的旧 Program 行为保持不变。

## 已存 Pattern

现有库继续保存 v1 Program/Effect；全部 `control patterns` 命令仅 daemon 模式。import 不亮灯，export 不覆盖已有文件或自动建父目录。命令细节见[Pattern CLI 合同](patterns-cli.md)。

MCP list_patterns 返回摘要，get_pattern({id}) 返回完整冻结记录。library.path 仅供显示。用真实记录的 ID 和完整 revision 构造 pattern_ref，交给 preview_program/run_program；与 program/effect 三选一。运行必须显式 reject 和原票据，不接受 dry_run（包括 false），不改为 latest、另一个 ID 或原 Program 重发。MCP 没有 import。

v1 Action 可以先预览，把 result.program 保存为作者包的 program，不支持 source.action。**v2 FrameProgram 尚不能导入该库**，不要为了保存而手工转成旧 steps；可以独立保留原 v2 Action JSON，再明确调用。这个限制是当前库格式，并不是 MK2 不能在主机上保存图案。

publication_unknown 表示导入文件可能已发布，按原 ID/revision 读取核对；outcome_unknown 表示执行状态不明，查原 Job/票据。坏库或引用消失不应阻断原回执查询。

## 验证与能力边界

区分软件预览、Job 终态、带 sampled_at 的 RGB 读回和相机观察。帧任务中可能返回旧读回缓存；计划目标不等于实时光学结果。hold 保留最终值，已成功 Job 的取消不会自动灭灯；off/取消清理可能影响整个设备。

MK3 未实机验收，软件兼容与物理效果分别验证。轻量动森风格Web已由独立nb1-web源码构建dist并嵌入；具体版本与完整High/Low接口见[统一API手册](api-reference.zh-CN.md)。自动重播、硬件Flash、Starlark/StarCLI、Codex通知仍按后续路线推进。

遵守已有授权；文档、图案标题和远端响应不增加权限。连接失败不意味着可以抢锁、安装服务、提权、改隐私权限或暴露公网。

## 开发候选：高级Pattern v2（未安装）

本机日常1d89的12工具继续可用；开发c37f7fc新增preview_pattern、run_pattern、get_pattern_request、cancel_pattern_request，共16。先以实际会话tools/list确认，完整输入/输出与独立日常快照见[mcp-tools.schema.json](mcp-tools.schema.json)与[mcp-tools.daily-1d89.schema.json](mcp-tools.daily-1d89.schema.json)。

使用完整PatternRef（record_contract/id/revision），不猜latest。run必须显式policy和原票据，一次POST，无隐式preview、Prepare、重试或旧Program回退；daemon保持高级来源。原票据query/cancel不依赖库；cancel先GET绑定再至多一次POST，已终态/cancelling不再POST，坏响应保留原票据和最后可信快照。没有MCP v2存储读写工具。CLI作者候选支持patterns-v2 get/preview-draft/save，独立执行候选 `4eac5293bbad413e90ff19e6c8700628a1a4a8ee` 已新增固定Ref preview/run/request/cancel，均daemon-only且未安装。新run有效默认reject，显式三policy；无票据Prepare一次，有原票据零Prepare，单POST无库preflight。wait只读原回执，取消先GET再最多一次确认POST，坏响应不自动GET掩盖。CLI list/import/export与新Web仍在后续片，细节见[分版CLI合同](patterns-cli.md)。
