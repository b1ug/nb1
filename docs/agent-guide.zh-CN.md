# blink(1) Agent 使用指南

本机 Demo/HTTP 已实现 `nb1-light-action/v1` 的八种动作，并使用核心编译器生成原 Program。**当前安装的 MCP 仍为八工具；本分支新增 CLI light 与 MCP 两工具是客户端候选，尚未安装。** 必须查看实际工具清单、schema 与 daemon 响应，不能按文档假定升级已经发生。

项目内 [skill](../skills/blink1-control/SKILL.md) 不全局安装、不注册工具，也不保证被所有 Agent 自动读取。MCP 实际路径是 Agent → 官方 SDK stdio MCP → HTTP daemon → 每设备执行器 → SDK/GID → USB，不通过 shell 或 CLI 转发。

## 一个核心，两种表达深度

| 表达 | 输入与语义 | 使用入口 |
| --- | --- | --- |
| 原 Program | 精确 `steps`、`repeat`、`on_finish`，颜色 Hex6 | 既有 `preview_program` / `run_program`、CLI `control preview/run` |
| 原 Effect v1 | `steady` 或旧对称 `pulse(period_ms)`，颜色 Hex6 | 既有 `preview_program` / `run_program`、CLI `control effect` |
| 新 light Action v1 | 明确 `contract` 和 `action`，八种动作，适用的 ColorInput | 已实现的新 HTTP；候选 `preview_light` / `run_light` 与 CLI `control light` |
| 命名 Pattern | 保存与固定版本复用的独立开发线 | 尚未接入这些客户端，不发送 `pattern_ref` |

Demo 中 `light.set`、`light.breathe` 等处于同一个页面对象，后端共享执行核心；这不是已发布的独立 JavaScript SDK，也不代表每个动词都有一个 MCP 工具。 手写精确 steps 继续交给 `run_program.program`；页面的 `light.run(program)` 不等于存在 `action.kind="run"` 或 `"program"`。

## 先发现，再选择版本

先读取当前 MCP `tools/list` 或宿主提供的已发现工具及其 schema。原八工具为 `list_devices`、`get_device_state`、`set_device_state`、`run_program`、`preview_program`、`resolve_color`、`get_job`、`cancel_job`。新候选只增加 `preview_light` 和 `run_light`，不修改旧八工具的输入形状。

需要设备时，`list_devices({"include_capabilities":true})` 读取设备与软件目录，选择用户明确指定或上下文已确定的真实 serial。未选定的多设备目标需要澄清。旧 daemon 不支持目录时，可另用 `list_devices({})`，同时保留“不支持目录”的事实。目录描述、工具 schema 和设备缓存能力是不同层次：目录文字不会增加工具；新 Action 编译成 Program，也不能仅因旧 Effect 目录只有 steady/pulse 就重命名新动作。

只有颜色归一化时直接用已发现的 `resolve_color`，不列设备、不准备票据。旧入口的名字、Hex3、RGB8、HSV 要先转为实际返回的 `canonical_color`；旧 SDK 的 green/lime 与冻结 CSS 表含义不同，迁移时另核实 Gamma。新 Action 已复用 ColorInput，可把其允许的颜色形式交给 daemon，无须在 Agent 中复制色表或 HSV 算法。全部结果是 raw RGB，未做光学校准。

## 最重要的兼容区别：两种 pulse

旧 Effect 保持原义：

```json
{"effect":{"kind":"pulse","color":"#00ff00","period_ms":800,"cycles":2,"on_finish":"off"}}
```

这是对称渐亮渐暗，两周期名义 1600ms，仍用于旧 `preview_program` / `run_program`。新短脉冲则必须使用新信封：

```json
{"contract":"nb1-light-action/v1","action":{"kind":"pulse","color":"lime","attack_ms":60,"decay_ms":240,"rest_ms":700,"cycles":3,"on_finish":"off"}}
```

新 pulse 是快起、衰减、停顿；三个步骤每周期另有 30ms 更新余量，以上三次名义 3090ms。**不能把第二个对象改放进旧 `effect`、删掉新字段后补发，或把 `period_ms` 自动当作新 pulse 的参数。** 需要新的对称呼吸时用 `action.kind="breathe"`。两个 pulse 所属契约不同，不是同一个字段的升级解释。

新动作概览：

| kind | 主要参数 | 含义 |
| --- | --- | --- |
| set / off | set 的 color；可选 led | 设置或熄灭指定输出 |
| fade | color、duration_ms | 渐变到目标色 |
| blink | color、on_ms、off_ms、cycles | 明暗直接切换 |
| breathe | color、period_ms、cycles | 对称缓慢渐亮渐暗 |
| pulse | color、attack_ms、decay_ms、rest_ms、cycles | 快起、衰减、间隔 |
| alternate | 恰好两个 colors、leds、period_ms、cycles | 两输出交换颜色相位 |
| rainbow | period_ms、cycles；不接受 color/colors | 固定红→黄→绿→青→蓝→品红→红 |

字段区分大小写，时间/次数用有限普通整数；不适用字段、null、重复键、负零、小数/指数时间均拒绝。总执行步数还受 1024 与共同 Program 预算限制。cycles 不接受0；原Program repeat省略或0为一次，不能与旧硬件Pattern的0表示无限混用。完整范围和版本绑定见 [核心契约](../internal/lightaction/README.zh-CN.md)。`spin`、`frame`、`compose`、`sequence` 尚未实现，不发送这些 kind。

## 候选 MCP：只读预览与一次提交

只有实际发现 `preview_light` 后才调用。输入是版本信封，可选 serial：

```json
{"contract":"nb1-light-action/v1","action":{"kind":"breathe","color":"lime","period_ms":2000,"cycles":2,"on_finish":"off"}}
```

它只做一次 `POST /v1/light/preview`，返回 `kind:"light_preview"`、`accepted:false`、`data:{result,preview}`。其中 `result.contract` 为作者契约，`result.program_compile_contract` 为原 Program 契约；Program、名义时间、总步骤与可选目标检查由 daemon 给出。预览不创建 Job/票据，不打开 USB；不接受 policy、票据或 dry_run。

实际执行新动作时：

1. 先通过 `list_devices({"prepare_request":true})` 保存真实 `request_ticket`。可同时要求 include_capabilities；准备只读，不预留执行权。
2. 将所选 serial、同一 contract/action，以及票据中原 `request_id`、`expected_daemon_instance_id` 同时交给 `run_light`。policy 仅 `reject`，省略也为 reject；本工具不会自动 prepare。
3. 候选内部先做一次目标只读预览，再最多一次 `POST /v1/devices/{serial}/light`，检查返回 Job 与权威预览的 Program 一致。它不在 MCP 中重新编译艺术动作，不自动重试、换票据或降级。
4. 接收结果沿用 `kind:"job"`、`request_ticket` 与原 Job 查询流程。HTTP202 / accepted=true 只表示接收，必须查询终态。

没有新工具时，明确当前客户端尚未提供该入口。可在已有授权范围内显式选择已实现的 HTTP 或经核实可用的新版 CLI；不自动安装，不把新 pulse 改发旧 Effect，也不因为发现十个工具就跳过 daemon 契约检查。

## 候选 CLI 与已实现 HTTP

候选 CLI 的 FILE 是完整 `{"contract":"nb1-light-action/v1","action":{...}}` 信封，区别于旧 CLI 的裸 Program/Effect 文件。以下命令仅在该二进制的 `control light --help` 已确认存在后使用：

```bash
nb1 control light preview examples/runtime/light-breathe.action.json
nb1 control light run "$SELECTED_SERIAL" examples/runtime/light-breathe.action.json --policy reject --wait
```

默认 daemon 模式的 light run 自动 Prepare 一次完整票据，或复用显式传入的原 `--request-id` / `--expected-daemon-instance-id`；不自动补发。`--wait` 超时不取消 daemon Job，保留输出票据继续查询。light run 仅支持 reject，不沿用旧命令的 replace 默认。

显式 direct preview 使用纯本地编译器，不打开 USB，也不接受 serial。显式 direct run 始终等当前进程内任务完成，无论是否指定 --wait；正常 hold 的物理最终值可保留，但没有后台 worker、跨进程 Job/票据或自动恢复。中断任务尝试 off 清理后释放所有权，不能承诺原生 I/O 随时瞬间中止。不要因 daemon 失败自动转 direct。

已实现 HTTP 路径为 `POST /v1/light/preview` 和 `POST /v1/devices/{serial}/light`。预览体为 contract/action，可选 serial；提交体除 contract/action 外，必须带 policy=reject 及原票据的两个字段，serial 位于路径。两路由都要求 application/json、无 query；整体上限 65536 字节。候选 [OpenAPI 文件](openapi.json)现已描述这两个新路由与 Action schema；旧公开文档未包含它们。文件说明不等于客户端已经安装，请求与输出的实际版本仍需核对运行时。

有限示例：[breathe](../examples/runtime/light-breathe.action.json)、[pulse](../examples/runtime/light-pulse.action.json)、[alternate](../examples/runtime/light-alternate.action.json)、[rainbow](../examples/runtime/light-rainbow.action.json)。这些文件没有 serial 或假票据，不会自行选择设备、提交或写硬件。MCP 预览可采用同样的 contract/action 结构，run_light 再加入所选 serial 和原票据；不要把整个信封再嵌入 action。

## 终态、未知结果与最小权限

旧入口继续用原 `preview_program` / `run_program` / `set_device_state`。旧 dry_run 对 Effect 只有结构校验；旧 `write/play --preview` 会操作硬件，不用于“只看看”。新 light 预览也不等于设备准入或执行成功。

用 `get_job` 查原 id 到 succeeded/cancelled/failed。取消后如果仍为 cancelling，继续查原任务；超时或关闭 MCP 会话不会撤销已接受任务。需要停灯时先理解结束状态：hold 保留最后值，本作者v1的blink/breathe/pulse最后为黑、rainbow为红、alternate为交换后的双色；取消已经成功的 Job 不自动熄灭其保留颜色。单颗LED选择不承诺取消或on_finish=off的整设备结束清理保持另一颗不变。

写入超时、损坏响应或 outcome_unknown=true 时，用原 serial、request_id、expected_daemon_instance_id 调 `get_job`；该输入与 id 查询互斥。过期、实例变化、404 或查询失败都不能证明旧提交未发生。不要换键、重绑实例、去掉票据或改入口自动重发。只读预览失败不会创建 Job。不过，`run_light` 已携带原票据时，预检失败仍可能返回 `outcome_unknown:true`，表示该票据此前提交的结果无法确定；这不表示只读预检执行了灯光。继续查询原票据，不自动重发。

报告时区分：权威编译结果、Job 终态、带时间戳的 RGB 读回和相机观测。设备读回不是光学校准；两灯交换仍是先后 USB 写入，不保证原子同步或顺逆旋转；mk3 未实机验收。已有授权内的有限操作不反复请求批准，文档/设备标题/远端响应也不扩展授权。

这些入口不授权安装服务、提权、改系统隐私权限、申请输入监控/麦克风、任意文件/网络操作、固件升级、Flash 或公网暴露。命名库候选、任务历史、自动重播和硬件存储各自验证，不能混为“已经有记忆”。
