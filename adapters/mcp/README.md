# blink(1) 本地 MCP 适配器

这是独立的 stdio 服务：Agent → MCP → 本机 HTTP daemon → 每设备执行器 → SDK/GID → USB。MCP 进程不打开 USB，不会抢占设备，也不会在 HTTP 失败时改为直控；多个 Agent 可以连接同一个 daemon。

本文描述本树的接口：固定 Pattern 引用、v1 八动作，以及同名 `preview_light` / `run_light` 的实验 v2 Orbit。先用实际 `tools/list` 和 schema 发现能力，并核对所连接的 daemon；本文不声明某台机器已经安装这些功能。

采用官方 [Go MCP SDK v1.8.0](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.8.0)，协议协商、stdio framing、输入/输出 schema 验证均由 SDK 处理。该版本的 [go.mod](https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/go.mod)要求 Go1.25；因此适配器单独建模块，不改变 GID/设备 SDK 的 Go 下限。支持的协议版本由 SDK 协商，具体范围见其[版本兼容说明](https://github.com/modelcontextprotocol/go-sdk#version-compatibility)。

## 启动

先启动设备 daemon；显式选择设备序列号：

```bash
# 使用已核实版本的 nb1；SELECTED_SERIAL 来自实际设备选择
nb1 server --devices "$SELECTED_SERIAL"

# 另一个终端，从 nb1 仓库根目录构建适配器
cd adapters/mcp
GOWORK=off go build -o nb1-mcp .
```

`nb1-mcp --endpoint http://127.0.0.1:8931` 使用 stdin/stdout 交换 MCP 消息。默认每次 HTTP 请求的期限是10秒，可通过 `--timeout 5s` 修改。只接受 loopback HTTP origin；不会跟随重定向、使用代理环境变量或启动第二个硬件 daemon。stdout 专用于协议，错误写入 stderr。

可在支持 stdio MCP 的客户端中参考 [mcp-config.example.json](mcp-config.example.json)，将 `command` 改成二进制的实际绝对路径。文件只是配置示例；构建不会自动修改客户端配置或注册工具。

## 工具

| 工具 | 输入与结果 |
| --- | --- |
| `list_devices` | 原 `{}` 返回设备数组。可选 `prepare_request:true` 额外读取实例信息，在输出外层加入 `request_ticket`；仅准备时 `data` 仍是设备数组。可选 `include_capabilities:true` 才查询软件能力目录，此时 `data` 为 `{devices,capabilities}`；两选项相互独立。准备不会创建任务或预留回执。 |
| `get_device_state` | `serial`。返回逻辑目标和带时间戳的原始 RGB 观测快照，保留其采样时间。 |
| `set_device_state` | `serial`、`color`，可选 `led`、`fade_ms`、`hold_ms`、`policy`、`dry_run` 及齐全的请求二元组。编译成完成后保留颜色的单步程序，返回 job。 |
| `run_program` | `serial` 和三选一的 `program` / `effect` / `pattern_ref`。原 Program/Effect 的 policy、dry_run、可选票据语义不变。引用分支必须显式 reject 和完整原票据，拒绝 dry_run 字段（包括 false），先目标预览再至多一次 jobs POST。 |
| `preview_program` | 三选一的 `program` / `effect` / `pattern_ref`，可选 `serial`。原分支返回 `program_preview`；引用分支返回 `pattern_preview`，data 为 `{pattern_ref,preview}`；均 `accepted:false`。不创建 Job/回执，设备检查只用缓存。 |
| `resolve_color` | 只读，必填 color：名字/hex3/hex6字符串、rgb8或hsv对象；HTTP归一化为 canonical_color 和 raw RGB，不发现设备或创建Job。 |
| `preview_light` | 明确 `contract` 和严格 `action`：v1 八动作，或 experimental-v2 的 orbit；可选 serial。一次只读高级编译 POST，返回 light_preview、accepted:false。 |
| `run_light` | 必填 serial、明确的 v1/v2 contract、action 及完整原票据。仅 reject；先目标预览，再至多一次高级执行 POST。v2 返回 FrameJob，不改发低层帧。 |
| `get_job` | `{id}` 自动按 framejob_ 前缀选帧路由；或原 serial/票据查询，帧任务须显式 job_kind:frame，省略/填写 program 是旧路由。两种输入互斥，不试探备用路由；回执有界且不跨重启保存。 |
| `cancel_job` | id 自动选择旧任务或 framejob_ 帧任务路由。请求取消后可能仍是 cancelling，尚不能认为硬件已停止。 |
| `list_patterns` | 仅空对象 `{}`；一次 `GET /v1/patterns`，返回 `pattern_list`、`accepted:false` 和库信息/有界摘要；不读本机路径、不准备票据、不播放。 |
| `get_pattern` | 仅 `{id}`；一次 `GET /v1/patterns/{id}`，校验完整冻结 Record 与内容 revision，返回 `pattern_record`、`accepted:false`。不准备票据、不执行、不打开响应中的本机路径。 |

输入仅接受声明的字段。原 Program/Effect/set 的 `color` 为原始 `#RRGGBB`，不隐式应用 Gamma；`led` 为0（全部）、1或2。原始 `program` 的 `fade_ms` 在0–600000之间且必须是10的倍数；`hold_ms` 在0–3600000之间。程序有1–1024步，repeat为0或省略时运行一次，最大10000次；每步的淡入、保持及10ms固件更新余量计入24小时名义上限，原生 I/O 仍有额外开销。`on_finish` 默认为 `off`，也可指定 `hold`。能力与实际支持型号以 daemon 结果为准，不据协议模拟声称 mk3 已经实测。

## 统一 light 作者动作

同一组工具按明确合同分派：`nb1-light-action/v1` 保留 set/off/fade/blink/breathe/pulse/alternate/rainbow；`nb1-light-action/experimental-v2` 提供高级 orbit，见下文“实验 Orbit 与帧任务”。调用者无需手写帧。`preview_light` 不接受票据、policy 或 dry_run；`run_light` 必须携带完整原票据，不接受 dry_run。

### v1 八动作

```json
{
  "contract": "nb1-light-action/v1",
  "action": {"kind":"breathe","color":"lime","period_ms":2000,"cycles":2},
  "serial": "20005601"
}
```

此例为 `preview_light`：仅一次 `POST /v1/light/preview`。省略 serial 只编译，提供 serial 只检查缓存能力。返回 `kind:"light_preview"`、`accepted:false`，data 保持 HTTP 的 `{result,preview}`：版本化规范动作、规范 Program、名义时长、总步骤以及目标验证。不会创建 Job、准备票据、查询设备列表、打开 USB 或自动重试。

运行时调用 `run_light`，在上述字段上添加从 `list_devices({prepare_request:true})` 保存的原 `request_id` 与 `expected_daemon_instance_id`。policy 缺省为 reject，也只允许 reject。适配器按顺序做：

1. 一次只读的目标 light 预览，验证合同、规范结构、原 serial、kind、非颜色参数与明确 hex 输入，要求目标为 supported。
2. 至多一次 `POST /v1/devices/{serial}/light`，使用未经重写的作者 Action、原票据及 reject。
3. 检查唯一实例/重放响应头、原设备、规范 Job.Program 与权威预览完全相同，以及 total/completed 步数范围。接受 Job 不等于完成。

不会自动准备、替换或刷新票据，不会用旧 jobs/direct 入口回退。预览失败就不发送执行 POST。任何被保留的旧票据可能涉及之前的尝试：本次没有发送执行 POST，不等于之前一定没有 Job。提交结果未知时返回原票据并保留 outcome_unknown，通过原 `get_job` 回执查询核对，不换键重发。

颜色仍由 daemon 权威归一化：复用 resolve_color 的字符串/rgb8/hsv 输入 schema，保留原 arguments 的数字词法。明确的 Hex3/Hex6 可独立绑定；CSS名字和HSV转换不在适配器中复制，也不宣称远端认证或独立重算。编译曲线只在 nb1 核心实现；适配器做结构、版本、参数、算术和响应一致性验证。

新 light 输入限65536字节。成功和失败的 HTTP 响应都要求恰好一个合法的 JSON `Content-Type`（可带 charset），并检查 UTF-8、重复字段、null、尾随数据及非配对 Unicode 转义；合法配对和普通转义仍接受。错误响应还要求封闭的 `{error:{code,message}}` 结构。损坏响应返回 `invalid_daemon_response`，不会从其中挑选一个错误码来断言已拒绝。标准 JSON 的404/501表示只读路由不支持；纯文本或损坏的404/501仍属于无效响应，不会触发旧接口回退。

`breathe` 是固定 cosine16 对称呼吸；新 `pulse` 默认60ms渐亮、240ms衰减和700ms停顿，名义周期另含3步的30ms开销。旧 `run_program.effect.kind=pulse` 的对称渐变语义完全保留。新作者动作执行总步数最多1024、总名义时长最多24小时；曲线、默认和精确时序见 [light 作者契约](../../internal/lightaction/README.zh-CN.md)。

软件测试使用官方 SDK 内存/stdio 和本地 HTTP fixture；运行记录须对应具体构建版本，不替代实机验收。MCP 进程不接管设备，模块不导入 nb1 核心或 USB 库。

### v1 alternate 的连续过渡

v1 继续使用同一 `preview_light` / `run_light`。此处的旧顺序过渡不等于实验 v2 的高级 orbit。
仅 `action.kind:"alternate"` 接受可选的
`transition:"cut"|"linear"|"sine"|"orbit"`。省略时规范动作仍不插入该字段，
使用旧的4步/周期；显式 `cut` 保留字段，也使用旧4步。其他动作拒绝此字段。

```json
{"contract":"nb1-light-action/v1","action":{"kind":"alternate","transition":"orbit","colors":["red","blue"],"period_ms":3000,"cycles":2},"serial":"20005601"}
```

`linear/sine/orbit` 使用66步/周期：起始两次写入和32组顺序更新的双灯目标。
周期为1300–1200000ms，最多15次（990步），仍受总执行1024步和24小时名义预算约束。
省略/`cut` 保持旧40ms最小周期。两灯写入有先后，不表示原子更新或真实三维旋转。

适配器验证版本、规范颜色参数、LED顺序、周期/次数/步数、闭环和64个顺序时段的
`fade/hold+10ms` 分配；linear/sine还核对首尾与半周期颜色锚点。
orbit的亮度/混色曲线与中间RGB由daemon权威编译，适配器不复制三角/正弦表，
也不声称独立校验完整数学曲线。执行响应的完整Program仍须与该次权威预览完全一致。
原票据、至多一次执行POST、未知结果查询和严格JSON规则均保持不变。
软件测试不等于已安装；先读取实际工具 schema，未声明 transition 的旧客户端不能接收此字段。

## Pattern 固定版本

`get_pattern` 读取完整记录，`preview_program` / `run_program` 支持与 Program/Effect 互斥的 pattern_ref。原分支语义保持；首先通过实际 schema 确认字段。没有 MCP 导入、覆盖或删除工具。现有 Pattern 库仍保存 v1 Program/Effect，不能把实验 v2 FrameProgram 直接导入；可独立保留原 Action 文件，不要手写旧 steps 伪装。

`list_patterns({})` 只接受空对象，省略 arguments 按 MCP 规则视为空对象。`id`、`path`、`limit`、`serial`、请求票据或任何其他字段都会在 HTTP 前拒绝。它只向已配置的 loopback daemon 发送一次 `GET /v1/patterns`，没有请求体、查询参数或后备路径；不访问设备、创建 Job、导入图案、改写文件、申请权限或自动重试。

成功返回 `kind:"pattern_list"`、`accepted:false`，data 保留服务端的 `schema_version:1`、`contract:"nb1-pattern-library/v1"`、`library` 和按 ASCII ID 排序的 `patterns` 摘要。library 必须为 ready/create-only、最多128条/1MiB，并声明现有记录与编译契约；绝对路径仅供显示，适配器不打开它。记录摘要验证 ID/标题/revision 形状、顺序唯一性、结束行为、能力与有限时间/步骤范围，但没有原始 Record 或 Program，所以不能重新计算 revision、验证来源展开或授予执行许可。

Pattern 沿用旧 Program 的限制：最多1024个定义步骤、repeat最多10000，总执行步数可能超过1024，但须满足每步至少10ms和总名义24小时限制。不要把新 light Action 的总执行步数1024上限套到旧保存内容。`source.effect.pulse` 仍是原对称呼吸，新 Action pulse 的语义没有迁移进 Pattern。

成功及失败响应都要求唯一 JSON Content-Type、有效UTF-8和无损Unicode转义，拒绝重复/未知字段、null、缺失字段、非整数或越界数据。服务不存在、未启用、损坏、busy或503保持明确错误，不伪装为空列表，不生成未知Job。服务合法返回 ready 和 `patterns:[]` 才代表空库（底层尚未创建目录也可以是合法空库）。

`get_pattern({id})` 取得完整冻结记录。成功返回 `kind:"pattern_record"`、`accepted:false`，data 为 Record 本身，包含 `id`、`title`、`source`、`compiled_program`、`compile_contract` 和完整 `revision`。适配器核对请求 ID、规范 Program、来源结构与 canonical revision；Program 来源须与冻结 Program 一致。旧 Effect 展开仍信任权威 Store，不在 MCP 复制旧效果编译器。内容哈希校验不等于服务端身份认证。

从实际 Record 取 ID/revision，再调用 `preview_program({pattern_ref:{id,revision},serial?})`。返回 `kind:"pattern_preview"`、`accepted:false`、`data:{pattern_ref,preview}`，仅一次只读 `POST /v1/programs/preview`。不准备票据；设备 supported 只说明缓存能力匹配，不保留设备或保证随后能提交。没有 `latest` 或自动升级引用。

执行引用时，`run_program` 必须带已选择的 serial、同一 `pattern_ref`、显式 `policy:"reject"` 和完整原 request_id / expected_daemon_instance_id。`dry_run` 字段不能出现，即使值为 false。先一次目标预览，检查原引用和支持状态；通过后至多一次 `POST /v1/devices/{serial}/jobs`。执行响应保留原 `kind:"job"`，并核对实例/重放响应头、设备、完整 Job.Program 与权威预览一致、总步数和进度边界。不会自动准备/刷新票据、重发执行或回退 raw Program、Effect、direct。

宿主伪代码如下，`call` 代表一次实际工具调用并取 structuredContent；选择值来自真实列表，不是新增工具：

```js
const listing = await call("list_patterns", {});
const selectedID = choosePatternID(listing.data.patterns);
const record = (await call("get_pattern", {id: selectedID})).data;
const ref = {id: record.id, revision: record.revision};
await call("preview_program", {pattern_ref: ref, serial: selectedSerial});
const ticket = (await call("list_devices", {prepare_request: true})).request_ticket;
// 保存完整 ticket 后，明确提交一次：
const result = await call("run_program", {
  serial: selectedSerial, pattern_ref: ref, policy: "reject", ...ticket
});
// 接纳并不等于完成；未知结果也用这对原值查询。
await call("get_job", {serial: selectedSerial, ...ticket});
```

引用预览/解析失败只表示本次未发执行 POST，不证明原票据之前没有 Job。引用运行错误保留原票据；`error.outcome_unknown` 为 true 时，查询原 `get_job`，不改 revision、实例或请求 ID 再试。库损坏或引用消失不阻断原 Job/回执查询。单独 `get_pattern`、`list_patterns` 和 `preview_program` 都是只读，不凭它们的失败生成一个新未知 Job。

## 可读颜色输入

先以实际 tools/list 和 schema 确认 resolve_color。颜色路由 `POST /v1/colors/resolve` 仅接收 `{"color":...}`，无 serial/policy/ticket；工具数量不能代替字段发现。

```json
{"color":"lime"}
```

```json
{"color":{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":50}}
```

成功外层 `kind:"color_resolution"`、`accepted:false`，data包含 schema_version=1、color_contract=nb1-color-input/v1、input_kind、canonical_color、rgb8，名字输入才有 palette=css-named-2026。把实际返回的hex放入原有Effect，再preview/run；原工具的color仍只收hex6。此计算不隐式Gamma、无USB、无回执；超时/损坏响应/5xx不产生未知Job，不自动重试或直控。旧路由404/501明确不支持。

适配器不复制色表/HSV算法；保留原JSON数字形式转发，由daemon严格校验普通整数/最多6位小数等词法。schema只做结构/范围检查，响应校验契约、规范hex/RGB自洽、输入种类和palette规则；不宣称远端来源认证或独立重算名字/HSV。完整语义和兼容限制见 [颜色输入v1](../../docs/color-input.zh-CN.md)。

## 高层效果

本节描述 `run_program` 原 Program/Effect 分支，两者互斥；新增固定引用是第三个互斥分支，见前文。原六工具的名称与调用方式保持兼容。效果仍提交到同一个 HTTP jobs 路由，MCP只发送意图，不自行展开 USB 指令或程序时序：

```json
{
  "serial": "20005601",
  "effect": {
    "kind": "pulse",
    "color": "#102030",
    "led": 0,
    "cycles": 2,
    "period_ms": 800,
    "on_finish": "off"
  },
  "dry_run": true
}
```

| 效果 | 结构字段与服务端默认 |
| --- | --- |
| `steady` | 必须 `kind`、`color`；可选 `led`、`fade_ms`（0–600000，10ms单位，省略0）。完成后保留颜色；拒绝 `cycles`、`period_ms`、`on_finish`，即使显式0或`hold`。 |
| `pulse` | 必须 `kind`、`color`；可选 `led`、`cycles`（1–10000，省略1）、`period_ms`（60–1200000，10ms单位，省略1000）、`on_finish`（`off`/`hold`，省略`off`）。拒绝 `fade_ms`，即使显式0。 |

MCP仅校验字段、枚举和基本范围，并将RGB规范为小写；省略的字段继续省略，由 daemon 的唯一权威 `CompileEffect` 应用默认、展开时序并校验总程序预算。效果的 `dry_run` 完全不发送 HTTP，`data` 明确包含 `compiled:false` 和 `validation_level:"structure_only"`；有请求二元组时还包含 `instance_binding_verified:false`。结构预览通过不表示 daemon 能在24小时预算内编译该意图，也不表示实例或准入期限已确认。实际提交后，`data.program` 如有则来自 daemon 的编译结果。

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

原始 `program` 的 `dry_run` 保持原形状：只做本地验证并返回规范化程序，完全不访问 HTTP 或 USB；效果预览的限制见上文。

## 权威软件预览与能力目录

`preview_program` 仍是只读、幂等提示工具；本节原 Program/Effect 的预览与原有 `dry_run` 语义不变。需要让 daemon 检查完整编译、总时长和结束行为时使用它：

```json
{
  "effect": {"kind": "pulse", "color": "#00a000", "cycles": 2, "period_ms": 800},
  "serial": "20005601"
}
```

它只发送一次 `POST /v1/programs/preview`，不调用 jobs、instance 或回执路由，不自动重试，不回退直控；不接受 `policy`、票据或 `dry_run`。Program、Effect、pattern_ref 每次三选一，后者的不同返回封装见前文。`serial` 可省略，此时完全不检查设备。相同的原始 Program、Effect 均由 daemon 的同一编译器处理，适配器不自行展开 Effect。800ms、两周期 pulse 的已知契约示例是 380/390ms 淡入、名义时长 1600ms、总步骤 6；这不是实时耗时保证。

响应必须 HTTP200 与 `application/json`（可带 charset），包含已知 `nb1-program-effect/v1`、规范 Program、正确的总步骤/名义时长、必需能力与一致的目标快照。目标状态为 `not_requested` / `supported` / `unsupported` / `unknown`；离线、未配置和缓存能力不可用不会被改判为支持。严格响应检查拒绝缺字段（包括零/false）、未知字段/大小写别名、重复键、显式 null、错误算术与不匹配 serial。原始 Program 的预览还必须保持已发送的规范 Program；Effect 的展开仍以 daemon 为权威，不增加第二编译器。

`list_devices` 使用 `{"include_capabilities":true}` 才额外 `GET /v1/capabilities`；默认与仅 `prepare_request:true` 的 `data` 数组形状保持不变。目录描述软件输入、效果、策略和上限，设备实际启用能力仍在各设备快照中。未来目录名称/存储标志可作为元数据呈现，不会自动启用新工具、硬件上传或 Pattern 库。

旧 daemon 的 404/501 明确返回 `unsupported`，不伪造目录或编译成功。预览的 HTTP POST 是只读计算：超时、损坏响应、5xx 等错误的 `outcome_unknown` 为 false（未输出时同为 false），不会追加“任务结果未知”警告；原来的 Job POST 和原票据查询继续保留其未知结果语义。MCP 工具注释是操作说明，不是权限边界。

能力目录与预览的软件测试可使用官方 SDK 内存/stdio 和本地 HTTP fixture，无需 USB。实际部署的工具、schema 与 daemon 版本仍须分别核对，不根据文档推断安装状态。

## 可选的请求回执

需要关联可能丢失响应的那一次提交时，先调用 `list_devices`：

```json
{"prepare_request": true}
```

它先读取设备，再通过 `GET /v1/instance` 取得实例和准入前缀；适配器只追加由 `crypto/rand` 生成的16字节 nonce。输出保持 `kind:"devices"` 和 `data` 数组，额外包含完整 `request_ticket`。从真实结果取值，不手工填写示意长 ID：

```js
const prepared = await call("list_devices", {prepare_request: true});
const ticket = prepared.request_ticket;
// ticket.request_id 和 ticket.expected_daemon_instance_id 原样保存。
```

保存它后，再把两个字段同时加入 `set_device_state` 或 `run_program`。原 Program/Effect 分支省略两字段仍走原提交路径；新的 pattern_ref 分支必须显式提供完整 pair。默认不准备 token、不重试写入、不更换实例或请求键，也不会在旧 daemon 不支持扩展时去掉字段降级。两种提交工具的 `IdempotentHint` 继续为 false，因为原分支的无键调用可以创建新任务。

适配器不主动重试提交；Go HTTP 传输层可能重试可安全重放的 GET。回执 GET 始终只读，不创建任务。

同一实例、设备、键和规范化 payload 在服务端有效回执期内映射至同一个逻辑 job；这不承诺 USB 指令恰好发送一次、任务必定成功或跨重启恢复。重放仍可能返回 HTTP202 和已经终结的原 job，应读取 `data.status`。二元组在提交结果和 HTTP/传输错误中以外层 `request_ticket` 原样保留。

带键成功响应还必须确认绑定：提交须为 HTTP202，带唯一且匹配的 `X-NB1-Daemon-Instance-ID`、值为 `true` 或 `false` 的唯一 `X-NB1-Replayed`，以及与输入 serial 相同的 `data.device`。回执查询须为 HTTP200，并确认相同实例与设备；不要求 replay header。缺失、重复或不匹配均作为 `invalid_daemon_response`、`outcome_unknown:true` 处理，不能把普通 job JSON 视为已确认的回执，也不会自动补发。无键工具保留原响应要求。

查找原提交时调用：

```js
await call("get_job", {serial: selectedSerial, ...ticket});
```

以上是 `get_job` 的另一种完整输入，不能同时提供 `id`。`request_expired`（410）、`daemon_instance_changed`（409）及回执查询的 `request_not_found`（404）保留 `outcome_unknown:true`：404只表示查询当时没有回执，原 POST 可能仍在途中。不要把旧意图自动换成新键或绑定到新实例；优先显式查询原票据，适配器不会为未知结果自动再次播放。

回执查询的任何失败都不能确认旧意图的执行结果，因此保留 `outcome_unknown:true`，包括网络错误、5xx、损坏响应，以及替换/降级 daemon 没有该路由时的通用404或501。原 `{id}` 查询和其他无键工具的错误语义保持不变；带键提交收到明确的首次400、429或 payload 冲突仍与不确定提交区分。

原 Program/Effect 分支有二元组的 `dry_run` 仍然零 HTTP，只检查字段格式及原始程序边界，效果仅检查结构；预览保留原二元组并明确返回 `instance_binding_verified:false`。它不能验证 daemon 实例是否匹配、回执是否存在、准入期限是否过期；期限是 daemon 单调运行时间，不能与客户端墙钟比较。pattern_ref 分支不接受 dry_run 字段，应使用只读 preview_program。

回执软件测试与实机验收必须按构建版本分别记录；模拟 HTTP 或 stdio 通过不能替代硬件结果。

## 任务与错误语义

### 实验 Orbit 与帧任务

`preview_light` 和 `run_light` 在同一个工具名下接受两份明确的合同：

- `nb1-light-action/v1`：现有八个动作及其原有行为。
- `nb1-light-action/experimental-v2`：独立的高层 `kind:"orbit"`，由 daemon 编译成新的 FrameProgram。`segments:4` 是简单长渐变候选，`segments:8` 保留更多目标细节。默认周期 3000ms、1 轮、红/蓝、`leds:[1,2]`、完成后 off。

```json
{"contract":"nb1-light-action/experimental-v2","action":{"kind":"orbit","colors":["red","blue"],"segments":4,"period_ms":3000,"cycles":3},"serial":"20005601"}
```

上例供 `preview_light` 使用，不会获取票据或创建任务。真正播放时，将同一 action 交给 `run_light`，附上调用方事先保存的完整 `request_id`、`expected_daemon_instance_id`，以及 `policy:"reject"`。适配器先请求一次 `/v2/light/preview`，确认所选设备的缓存能力，再最多提交一次 `/v2/devices/{serial}/light`；不会把高层动作替换成低层帧提交，也不会重建颜色曲线。低层 `run_program` 工具及旧 Program 仍保留。

返回仍是 `kind:"light_preview"` 或 `kind:"job"`。V2 Job 的 `program.contract` 为 `nb1-frame-program/experimental-v1`，进度字段为 `completed_frames` / `total_frames` 和 `completed_target_writes` / `planned_target_writes`。帧数满不等于清理完成，应等实际终态。

`get_job` / `cancel_job` 自动按严格的 `framejob_` ID 走帧任务路由。只有原票据、没有 Job ID 时，必须为帧任务明确加上 `job_kind:"frame"`：

```js
await call("get_job", {serial: selectedSerial, job_kind: "frame", ...ticket});
```

省略 `job_kind` 或填写 `program` 仍查询旧 Program 回执；不会试探第二个路由。不能同时提供 `id` 和 `job_kind`。错误、丢失响应、过期或重启都不会触发自动换票或再次播放。

适配器验证完整帧格式、整数预算、相位时间、LED 顺序、周期闭合、规范动作与原参数绑定，以及 Job / header / 原票据一致性。名称和 HSV 的解析、具体颜色曲线与实机质量仍由 daemon 和物理验收负责；这些检查不包含第二份效果编译器或光学测量。

返回值同时包含 `structuredContent` 和 JSON 文本兼容内容。`kind` 区分设备列表、状态、job、预览和错误，`data` 保留 daemon 的原始结构。

- `accepted=true` 仅表示看到了 HTTP202，并不表示执行完成。`data.status` 是实际任务状态；非终态结果提供 `poll_after_ms=200` 作为建议。
- `queued`、`running`、`cancelling` 需要继续查询；`succeeded`、`cancelled`、`failed` 是终态。取消后要等到 `cancelled` 才表示执行器确认硬件清理；清理失败会保留 `failed`。
- HTTP 错误保留 `error.code`、`message`、`http_status` 并设置 MCP `isError=true`。输入 schema 错误由官方 SDK 返回 `isError`。
- 提交 Job 的 POST 超时或响应损坏时，可能已被 daemon 接收：`error.outcome_unknown=true`，不会盲目重试。此时 `accepted=false` 只表示没有确认到202，不能解释为未执行。应查询设备/任务，必要时显式停止已有任务。
- MCP 请求超时或 MCP 会话关闭，不会替用户取消 daemon 中已接受的任务。请求取消应调用 `cancel_job`。

本适配器不提供固件升级、Flash 写入、任意 HID 报文、系统服务安装或公网控制。

## 验证

```bash
GOWORK=off go test -race -count=1 -timeout=4m ./...
GOWORK=off go vet ./...
```

测试用模拟 HTTP 服务，并用官方 SDK 客户端覆盖工具发现、原始参数歧义与 schema 拦截、程序预览、真实 stdio 子进程通信、队列错误、取消中状态、POST 超时歧义及重定向/响应上限。重复键、嵌套 null、大小写别名和输入限制用例确认在任何 HTTP 请求前被拒绝。不会打开真实 USB。硬件链路应由主工作区另行验收。

回执测试同样使用官方 stdio 客户端，覆盖原有工具输入、准备键的随机性、成对字段与查询互斥、原 payload/键保留、终态202、响应header和设备绑定、查询失败的未知结果、POST丢失响应不重试，以及带键 dry-run 零 HTTP。没有引入额外 Go 依赖或提高模块 Go1.25 下限。

下列跨入口测试用于核对工具发现、完整 Record、固定引用、原票据跨 MCP/CLI 查询及坏库恢复。应按运行的源码和构建版本保留记录，不将其他版本的软件或实机结果冒作本次验收。

效果测试覆盖官方 stdio 下的原始程序兼容、program/effect二选一、重复键/null/大小写/结构范围拦截、纯意图转发、回执与响应绑定保留，以及效果结构预览零 HTTP。MCP 未引入核心编译器或设备依赖；此类软件测试不等同于效果硬件验收。

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

`TestLiveEffectsOptIn` 同样默认跳过，需另设 `NB1_MCP_LIVE_EFFECTS=1` 和显式 endpoint/serial/color。它只提交有限的2次、每周期800ms的 pulse，等待 daemon job 成功，检查最终双LED原始RGB熄灭，并另发独立 off 清理；不会据此宣称摄像头已观察到脉冲：

```bash
NB1_MCP_LIVE_EFFECTS=1 \
NB1_MCP_LIVE_ENDPOINT=http://127.0.0.1:8931 \
NB1_MCP_LIVE_SERIAL=20005601 \
NB1_MCP_LIVE_COLOR='#000000' \
GOWORK=off go test -run '^TestLiveEffectsOptIn$' -v
```

接口依据为仓库的 `internal/control/types.go`、`internal/control/frame_service.go` 与 `internal/httpapi`。适配器当前保留独立 wire 类型和相同校验，避免导入 HID 模块；今后接口演进需要共享 schema/契约测试，不应单边修改。
