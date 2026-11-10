# nb1 完整 API 手册：High level、Low level 与当前状态

更新：2026-11-10。这份手册描述当前代码与本机已安装版本，供接口评审和实际调用使用。本轮没有发布新 Release（历史v0.0.1预发布仍存在）；`experimental` 合同仍是实验接口。

范围是nb1当前完整HTTP面及对应CLI/MCP、执行模型和存储合同。GID与blink1-go属于独立HID/硬件SDK层，见后面的来源索引；本手册不会把它们的raw report/Flash能力虚构为HTTP端点。`internal/control`的Go接口供nb1内部接线，不是可以从任意外部Go模块直接导入的独立SDK。

## 1. 现在分别完成了什么

| 层次 | 已实现的能力 | 当前可用状态 | 尚未完成 |
| --- | --- | --- | --- |
| Low level · Program | 精确 RGB、LED、fade/hold、步骤序列、有限重复、队列、回执、取消、结束清理 | 日常 `1d89eae` 已有 HTTP、CLI direct/daemon、MCP；当前 Mac/MK2 有有限实机证据 | 其他系统实物 USB、MK3、原生阻塞的统一硬截止时间 |
| Low level · FrameProgram | 一帧内两灯重叠渐变、初始化一次、帧间隔、有限重复、帧进度与取消 | 日常版本有实验 HTTP/Go 执行器；High level Orbit、CLI 和 MCP 已使用它 | 稳定版承诺、多设备同步、其他代际实机验收 |
| High level · light v1 | `set`、`off`、`fade`、`blink`、`breathe`、`pulse`、`alternate`、`rainbow` | 八动作已经实现，HTTP/CLI/MCP/Web 共用核心编译器 | 动作命名最终定稿、更多艺术动作与组合语法 |
| High level · Orbit v2 | `orbit`，4/8 段成对长渐变；颜色、相位、周期、轮数、结束规则 | 日常版本已可用；Web、CLI、MCP 各有有限 Mac/MK2 验证 | 不是稳定视觉标准；不保证任意参数完全无闪烁 |
| 记忆 · Pattern v1 | 电脑端保存 Program/旧 Effect、导入导出、精确 revision 引用 | 已接 Web、HTTP、CLI、MCP | 不能直接保存新的 FrameProgram/Orbit 来源 |
| 记忆 · Pattern v2 | 同时保存原始高级来源、规范动作、recipe、冻结执行体与内容摘要 | 开发源码已完成codec/Store/创作/来源准入/P5/P6；MCP四工具、CLI七命令与高级图案书Web已接入候选96a；本机MK2有限实机验证后已工作区推广；实际stdio16，不自动注册宿主 | CLI list/import/export、更多参数与平台/代际验收 |

本文保留的历史日常基线是 `1d89eae92278f24a68fff1a34e3c1e696a962c3c`，整合候选为 `96a1f92a8a2fcf836129797e4c3a82602a4e7ffc`（简称96a），包含P1–P6、独立v2库、CLI七命令、MCP16与高级图案书Web。HTTP核心的历史资格来源f8bb267、host准备来源56c4aef、CLI4e及MCPc37/679分别保留；不是各入口仍未整合。96a在单一临时窗口验证后曾恢复1d89，随后完成工作区推广；当前安装见下面独立状态块。日常硬件为 MK2 / FW204；这不证明 MK3 或 Windows/Linux 实物 USB 已验收。

历史Web真机验证包括Pulse/Orbit完成与取消；早期CLI/MCP实机证据单独绑定c4147ce。新增96a三入口有限MK2实机证据见下段，不把旧证据改称新版本。P6 相关六包 current/Go1.18 各 1412 测试节点、339 顶层通过，race 四包1097/263通过；新 P6 用 Recorder、内存驱动和临时库，既有整包回归可能用私有 loopback fixture listener。它们不构成新实体设备验收，RGB读回和摄像观测证据仍单独记录。

### 工作区部署状态（独立于源码资格）

<!-- deployment-state:start -->
2026-11-10工作区已推广并安装固定96a原生二进制：`bin/nb1`、`bin/nb1-runtime`、`bin/nb1-mcp`，本机daemon为`http://127.0.0.1:8931`。33项只读复核通过：CLI七命令、真实stdio16工具、扩展bundle10项manifest.files（9公开资源+LICENSE）及逐字节匹配、设备online/idle/黑色缓存；没有新增设备变更，原v1快照SHA前后一致。旧1d89备份保留，未创建OS开机服务、注册全局MCP、合并PR或发布Release。

部署记录为本地`artifacts/pattern-v2-suite-native-20261110/install-result.json`、`promotion.json`、`post-install.json`和`post-install-stdio.json`。MCP16表示已安装适配器的实际stdio会话，宿主既有会话仍须重新发现，不会因文档更新自动注册工具。此后docs-only提交的源码号不改变实机binary资格96a及其SHA。
<!-- deployment-state:end -->

下文“日常1d89”“开发f8bb”或“原片未安装”均指其明确的历史基线；当前工作区部署只按上面这段与实际帮助/会话tools/list核对。源码有能力不等于安装已生效。

### 96a候选的本机实物证据

2026-11-10，macOS arm64、MK2/FW204、SDK b3f2586/GID37d7cfd，在隔离v2库和单一设备所有权窗口完成CLI 3项、真实SDK stdio MCP 2项、实际嵌入Web 2项，共7个任务、3次运行中取消。任务通过原Ref/票据查询终态，随后有新鲜双黑RGB读回、无活动/排队；结束后正规停止候选并恢复1d89。CLI作者预览/保存/读取与MCP纯预览没有设备变更。不是安装、发布或全平台验收。

静默私有录像32.766667秒、983帧，没有超过50ms的PTS间隙；有限窗口未见中途全黑帧，亮边有过曝。帧时间连续不代表USB指令等间隔，也不能证明完美360度、无闪烁或已校准Gamma。该窗口USB记录6次retry（query 3、set 2、stop 1），最终失败0；原因和是否重触发fade仍待诊断，不能称零重试。

候选nb1 SHA256 `b5a0f116b2f9c1b0047ef43960fb227148e29edd6b293ecee15dc45eecb3f4d3`，MCP SHA256 `8393167a18e7645561dff82b99698615e6e77a4361358af26a593939b0898f1d`；二者clean source均96a。公开脱敏资格为本地 `artifacts/pattern-v2-suite-native-20261110/qualification.json`（状态FINITE_MAC_MK2_SUITE_QUALIFIED_WITH_LIMITS）；原始执行记录为同目录attempt1/report.json，光学分析另存attempt1。raw录像保持私有，不随文档发布；报告的transport通过和光学限制分别保留。

### 后续候选：诊断与高级图案列表

开发片已增加第8个CLI命令 `control patterns-v2 list` 及默认关闭的 `server --transport-trace`，当前安装96a仍是七命令且没有此诊断flag。干净候选bc782在临时Mac/MK2窗口完成Pulse、Orbit和运行中取消，196条trace边/98对SDK调用均能对应原Ref和真实位置；零重试、零失败，结束后恢复日常96a并关闭trace。

名义750ms的Orbit帧起点实测间隔751.059–752.173ms，同帧两颗灯仍先后写入；SDK返回后两目标间隔10.114–12.484ms。整个Orbit任务6080.978ms，包括主机、USB与清理开销。本轮录像里灯在画面外，因此**只验证指令、时序与清理，不作灯效验收**；背景裁剪的亮度统计已撤回。零重试样本也不能解释上一窗口的6次恢复重试。脱敏结果见本地`artifacts/transport-trace-native-20261110/qualification.json`和`summary.zh-CN.md`；原录像与日志保持私有。诊断限制见[本地运行说明](local-runtime.md#后续开发候选的诊断开关)。

## 2. High level 与 Low level 的职责

**High level 描述想要的效果；Low level 描述怎样执行。** 两者都保留，并共用一个设备所有者和 worker。

```text
Web / CLI / MCP / HTTP
          │
          ├─ light Action：blink / breathe / pulse / orbit …
          │       └─ nb1 核心编译
          │
          └─ 精确 Program / FrameProgram
                         │
                同一设备队列与任务回执
                         │
                 SDK → GID → USB
```

High level 不是让调用者循环发低级请求。一次高级请求由 nb1 编译、排队和计时；设备执行 fade，主机控制序列与间隔。USB 写入仍有先后，不是一次把任意高级动画全部写入硬件自主运行。

基础动作 `set/off/fade` 也放在 `light` 中，因此用户不需要为了设一个颜色先学习步骤模型。需要精确控制每一步时，再使用 Program；需要一阶段两灯重叠渐变时使用 FrameProgram。

页面里的 `light.breathe({...})` 是便于阅读的表示法，**目前没有承诺同名 JavaScript SDK**。实际 HTTP 输入是明确的 `contract + action`；CLI 读取同一 JSON，MCP 使用 `preview_light/run_light`。

## 3. 通用调用规则

- 默认 HTTP origin 为 `http://127.0.0.1:8931`。服务只接受本地 Host 和匹配的浏览器 Origin，没有新增跨域控制或用户认证。
- JSON 请求使用 `Content-Type: application/json`；字段名区分大小写。严格入口拒绝未知/重复字段、显式 `null`、尾随值；具体大小和词法限制见各模型。
- `serial` 取自实际设备列表，示例 `20005601` 不是所有设备通用值。新校验器允许 1–128 个 ASCII 字母、数字、`-`、`_`；连接到设备仍须使用已配置的真实序列号。
- 时间单位均为毫秒，色值为原始 RGB8，没有默认软件 Gamma 或亮度校准。名义时长不包含原生 USB 与系统调度的全部耗时。
- `HTTP 202` 只表示任务已接受；只有可信任务终态说明执行或清理结果。
- CLI 默认使用 daemon；显式 `--mode direct` 在当前进程复用同一核心与所有权锁。连接失败不会自动改为直控。

CLI `control`的`--timeout`默认30s；同一次命令中的Prepare、HTTP提交与`--wait`共用一个context预算。长动作可显式加长，例如`nb1 control --timeout 2m patterns-v2 run SERIAL ID --revision SHA --wait`；真实SERIAL/revision须来自设备与记录。超时后daemon任务仍可继续，保留原票据并明确request恢复，不能据此换键重播。

纯预览无 Job、无运行票据、无 USB。带 `serial` 的预览只检查已配置设备的缓存快照，不预留设备，也不保证稍后的提交一定成功。

## 4. High level：light v1 的完整语法

```json
{
  "contract": "nb1-light-action/v1",
  "action": {
    "kind": "breathe",
    "color": "lime",
    "led": 0,
    "period_ms": 2000,
    "cycles": 2,
    "on_finish": "off"
  }
}
```

作者文件只包含 `contract/action`。HTTP 预览还可加 `serial`；运行还须加 `policy/request_id/expected_daemon_instance_id`。

所有动作都可指定 `on_finish: off|hold`。`set/off/fade` 默认 `hold`，其他动作默认 `off`。除 `alternate` 外，`led` 默认为 0，可取 0/1/2；0 为固件共同输出选择。

| kind | 含义 | 专属字段、默认值与范围 | 编译后的名义时间 |
| --- | --- | --- | --- |
| `set` | 直接设色 | 必填 `color` | 10ms |
| `off` | 把所选输出设黑 | 不接受 color | 10ms |
| `fade` | 向目标颜色渐变 | 必填 color、`duration_ms`；10–600000，10ms 倍数 | duration + 10ms |
| `blink` | 亮灭闪烁，边沿不渐变 | 必填 color；`on_ms:250`、`off_ms:250`，各10–3600000；`cycles:1` | (on + off) × cycles |
| `breathe` | 对称、平滑的强度起伏 | 必填 color；`period_ms:2000`，330–1200000；`cycles:1` | period × cycles |
| `pulse` | 快速亮起、衰减、停顿 | 必填 color；`attack_ms:60`、`decay_ms:240`，各10–600000且10ms倍数；`rest_ms:700`，0–3600000；`cycles:1` | (attack + decay + rest + 30) × cycles |
| `alternate` | 两颗灯交换两种颜色 | `colors:[red,blue]`；`leds:[1,2]`或[2,1]；`period_ms:2000`；`cycles:1`；可选 transition | period × cycles |
| `rainbow` | 红黄绿青蓝品红的色相循环 | 不接受 color/colors；`period_ms:3000`，130–1200000；`cycles:1` | period × cycles |

`cycles` 语法范围为 1–10000，但高级 v1 **总执行步数不能超过 1024**，编译后还须在24小时名义预算内。各动作不能使用别的动作字段，例如 breathe 不接受 attack_ms，rainbow 不接受 color。

高级Action的时间/次数必须为普通非负 JSON 整数；不接受 `1.0`、`1e3`、负零或字符串数字。每份请求最多 65536 字节，作者 JSON 最深32层。HSV 小数另按颜色规则处理。

### alternate 的兼容过渡

`transition` 仅 alternate 接受：省略或 `cut` 使用原四步交替，周期40–1200000ms；`linear/sine/orbit` 是保留的旧66步/周期实验，周期1300–1200000ms，最多15轮。

旧 `alternate.transition:orbit` 与下文新 `kind:orbit` **不是相同执行方式**。旧文件、旧 Program 和已存图案不会自动升级。需要当前成对长渐变环绕时，明确选择新实验合同。

`alternate` 仍是当前名字；`switch/swap/spin/sequence/compose` 尚未成为新的已实现 action.kind。名称可以继续评审，不能在文档里把候选词当作可调用接口。

### hold 的实际结果

`hold` 保留的是**最后一步的输出**。blink/breathe/pulse 最后通常已是黑色；rainbow 最后为红色；cut alternate 为交换后的双色。它不等于“保持输入的 color”。`off` 则在结束清理中关闭设备输出。

结束或活动取消时的off清理是设备级操作，不能承诺只熄灭该Action选择的某一颗LED。

## 5. High level：实验 Orbit v2

```json
{
  "contract": "nb1-light-action/experimental-v2",
  "action": {
    "kind": "orbit",
    "colors": ["red", "blue"],
    "leds": [1, 2],
    "period_ms": 3000,
    "cycles": 2,
    "segments": 4,
    "on_finish": "off"
  }
}
```

| 字段 | 默认与规则 |
| --- | --- |
| kind | 必须为 orbit；v2 不自动接收 v1 动作 |
| colors | 默认 red/blue；恰好两个 ColorInput |
| leds | 默认 [1,2]，也可 [2,1]；顺序是逻辑相位，不证明可辨顺逆旋转 |
| period_ms | 默认3000；每段间隔50–600000；四段总范围200–2400000，八段400–4800000 |
| cycles | 默认1；四段1–256，八段1–128；总名义时长仍须≤24小时 |
| segments | 默认4，只能4或8 |
| on_finish | 默认off，也可hold |

返回规范 action、`recipe_id`、冻结 FrameProgram、总帧数、计划设色次数和名义时间。四段配方为 `orbit-paired-linear-4/experimental-v1`，八段对应 `…-8/…`。

每段目标来自固定曲线样本，设备在目标之间做长线性 fade，不是大量很短 fade 的逐灯远程模拟。两灯 USB 写入约定有10ms间距；初始化只执行一次，repeat 只重复 frames。fade 向上量化为10ms倍数，末帧渐变完成后才清理/读回。

3000ms、单轮的默认四段名义时长为3040ms，八段3045ms；额外来自初始化、末段量化及等待，不是宣称实测恰好等于这些值。设备外壳、PWM、曝光仍影响观感；本机有限录像不能证明所有参数都完全无闪烁。

## 6. 颜色输入：哪些入口更灵活

High level light 和 `resolve_color` 支持下表；旧 Program/Effect、CLI `control set` 与MCP `set_device_state`的执行颜色仍只收 `#RRGGBB`。高级`light.kind:set`支持完整ColorInput，不能与这些旧set入口混淆。

| 输入 | 示例 | 结果/规则 |
| --- | --- | --- |
| Hex6 | `"#12AbEF"` | `#12abef` |
| Hex3 | `"#0f8"` | `#00ff88` |
| 名字 | `"green"`、`"lime"` | 固定148个不透明CSS名字；分别#008000、#00ff00 |
| RGB8 | `{"space":"rgb8","r":0,"g":255,"b":136}` | 通道0–255普通整数；不接受alpha |
| HSV | `{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":50}` | h:0–360，s/v:0–100；结果#008000 |

HSV 使用普通非负十进制整数或最多6位小数，禁止指数、负零、NaN、Infinity；360°等同0°。转换采用精确十进制运算，最后一次 half-up 量化为RGB8。

名字仅 ASCII 大小写不敏感，外层去 ASCII 空白。字符串最多64个解码字节；独立颜色请求最多4096字节。没有 rgb() 字符串、HSL、alpha、transparent、currentColor、中文别名、CSS变量、random 或用户色表。范围越界拒绝，不夹取。

```http
POST /v1/colors/resolve
Content-Type: application/json

{"color":{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":50}}
```

```json
{"schema_version":1,"color_contract":"nb1-color-input/v1","input_kind":"hsv","canonical_color":"#008000","rgb8":{"r":0,"g":128,"b":0}}
```

只有名字结果带 `palette:css-named-2026`。颜色归一化不亮灯；需要低层调用时，把返回的 canonical_color 放入 Program。旧 SDK 色表/Gamma 与此合同有差异，不能依赖旧配置产生同样视觉效果。

## 7. Low level：Program 的语法与时序

Program 没有新的 contract 外壳，语义标识由能力/预览中的 `compile_contract:nb1-program-effect/v1` 声明。

```json
{
  "steps": [
    {"led":1,"color":"#ff0000","fade_ms":400,"hold_ms":600},
    {"led":2,"color":"#0000ff","fade_ms":400,"hold_ms":600}
  ],
  "repeat":2,
  "on_finish":"off"
}
```

| 字段 | 规则 |
| --- | --- |
| steps | 1–1024个定义步骤；依次执行 |
| led | 0/1/2；省略为0 |
| color | 必填六位#RRGGBB，接受大写并规范为小写；不收名字/Hex3/HSV |
| fade_ms | 默认0；0–600000，必须10ms倍数 |
| hold_ms | 默认0；0–3600000 |
| repeat | 省略或0规范为1；最大10000；0不代表无限 |
| on_finish | 省略或空串规范为off；也可hold；规范输出只保留off/hold |

每一步名义等待 `fade_ms + hold_ms + 10ms`。例子每轮2020ms，2轮4040ms，USB和清理另有开销。Program 的1024限制是**定义步骤数**；与高级 Action 的1024**总执行步数**不同。最终总名义时长仍不能超过24小时。

旧Program/Effect HTTP通过Go int解码：小数或指数整数拼写拒绝；`-0`会先成为0，再按字段范围/默认校验。不要把高级Action/Pattern作者对原始负零的拒绝推广到该旧入口。新代码应发送普通非负整数字面量。

Program 是顺序模型：第一步等待完成后才进入第二步。把两个LED分别写成两步，不会形成同时淡变。需要重叠渐变使用 FrameProgram，或让 High level Orbit 为你编译。

预览会列出set/program/raw-rgb等要求，hold还列hold；当前legacy Submit并未统一把所有广告能力作为强制门槛。能力预览与实际准入检查的差异是待完善项，不能把目录直接当成执行保证。

预览：`POST /v1/programs/preview`，输入 `{"program":PROGRAM,"serial":"选定设备，可省略"}`。运行：`POST /v1/devices/{serial}/jobs`，输入 `{"program":PROGRAM,"policy":"reject"}`，可附完整原票据。**没有独立的 HTTP /set 或 /state 路由**；读状态用`GET /v1/devices/{serial}`，CLI set 生成完成后hold的单步Program再提交jobs。

## 8. Low level：实验 FrameProgram

```json
{
  "contract":"nb1-frame-program/experimental-v1",
  "initial":[{"led":1,"color":"#ff0000"},{"led":2,"color":"#0000ff"}],
  "frames":[
    {"targets":[{"led":1,"color":"#0000ff"},{"led":2,"color":"#ff0000"}],"fade_ms":500,"interval_ms":500},
    {"targets":[{"led":1,"color":"#ff0000"},{"led":2,"color":"#0000ff"}],"fade_ms":500,"interval_ms":500}
  ],
  "repeat":2,
  "on_finish":"off"
}
```

| 字段 | 规则 |
| --- | --- |
| contract | 必须nb1-frame-program/experimental-v1 |
| initial | 1–2个物理LED目标，LED只能1/2且不重复；初始化只执行一次 |
| frames | 1–1024个定义帧；每帧目标数、LED顺序须与initial相同 |
| targets[].color | 原始Hex6 |
| fade_ms | HTTP必填，可显式0；0–600000，10ms倍数，不能超过该帧interval向上取整至10ms后的值 |
| interval_ms | HTTP必填；50–600000 |
| repeat | HTTP省略为1，显式值1–10000；执行总帧数仍≤1024；HTTP显式0拒绝 |
| on_finish | 默认off，或hold |

执行总目标写入最多2050，名义时间最多24小时，输入最多1MiB。每帧依次设置目标，等待同一帧的剩余间隔；不会分别等待两个LED的完整fade。迟到不突发追帧，最后fade完成后才结束。

内部Go `NormalizeFrameProgram` 可把typed零值repeat/空on_finish补默认；HTTP严格decoder仅对repeat/on_finish省略时补默认，显式repeat0或空on_finish拒绝；每个target的led/color与每帧fade_ms/interval_ms均必填。raw Frame的整数仍经Go int，fade_ms:-0等价0；高级Orbit/Pattern作者另有更严格数字词法。不要把内部函数的容错范围当成HTTP语法。

预览为 `POST /v1/frame-programs/preview`，body `{"program":FRAME_PROGRAM,"serial":"可省略"}`；运行用 `/v1/devices/{serial}/frame-jobs`。原始帧和当前高级 light 运行都要求显式 reject 与完整原票据。

原始FrameProgram当前主要通过实验HTTP调用；内部Go接口供nb1接线。CLI/MCP通过高级Orbit间接使用帧执行器，尚无独立的raw FrameProgram命令或工具。

Frame 执行能力单列于 `GET /v1/frame-capabilities`；普通 v1 capabilities 目录不能代替这项查询。当前缓存能力gate要求set/raw-rgb/mk2-basic；hold会列入预览要求，但该helper没有另做hold检查。不会仅凭固件版本推断MK3实机支持。

## 9. 兼容保留：旧 Effect

这是旧 `/jobs` 与 `preview_program/run_program` 的简写，不是新版 light 的全部动作。

| kind | 输入与语义 |
| --- | --- |
| steady | 必填kind/color；可选led、fade_ms；最终hold；拒绝cycles/period_ms/on_finish |
| pulse | 必填kind/color；可选led、cycles默认1、period_ms默认1000（60–1200000且10ms倍数）、on_finish默认off；拒绝fade_ms |

旧 Effect pulse 是对称渐亮/渐暗；新版 light pulse 用 attack/decay/rest 表达不同节奏；新版 breathe 表达对称呼吸。明确区分入口与contract，不改旧程序或图案的意义。

```json
{"effect":{"kind":"pulse","color":"#00ff00","period_ms":1000,"cycles":2}}
```

在 `/v1/programs/preview` 中 program/effect/pattern_ref 必须三选一。只有结构 dry_run 不等于权威编译，更不等于执行支持。

## 10. 从预览到播放：票据、任务与取消

### 为什么有长 daemon ID

`daemon_instance_id` 标识一个进程生命周期，`request_id` 标识一次提交。它们避免响应丢失时，把旧意图误投给重启后的进程。业务动作里不必手写它们：CLI自动准备一次，MCP调用方保存实际准备结果，Web保存在当前操作中。

这不是账号、设备序列号、永久Pattern ID或跨重启任务数据库。准备票据不亮灯，也不预占设备/回执容量。

### HTTP 流程

1. `GET /v1/devices` 选择serial；按需读capabilities与设备缓存。
2. 对所选合同调用只读preview；检查规范化结果、能力和名义预算。
3. `GET /v1/instance` 取得daemon_instance_id和idempotency.request_id_prefix；前缀后追加32个随机小写hex字符，原样保存二元组。
4. 带同一原意图和票据，提交一次。高级v1走 `/v1/devices/{serial}/light`；Orbit v2走 `/v2/devices/{serial}/light`。
5. 检查202、唯一且匹配的实例/重放响应头和完整Job，再查询原回执到终态。

响应头：`X-NB1-Daemon-Instance-ID`、`X-NB1-Replayed:true|false`，Location指向原回执。准入期限基于Engine单调uptime，不是墙上日期。不要手工复制过期示例键。

| 任务模型 | 原回执查询 | ID查询 | 请求取消 |
| --- | --- | --- | --- |
| Program | GET /v1/devices/{serial}/requests/{request_id}?expected_daemon_instance_id=原实例 | GET /v1/jobs/{jobId} | POST /v1/jobs/{jobId}/cancel |
| Frame/Orbit v2 | GET /v1/devices/{serial}/frame-requests/{request_id}?expected_daemon_instance_id=原实例 | GET /v1/frame-jobs/{jobId} | POST /v1/frame-jobs/{jobId}/cancel |

帧取消可在JSON中带 expected_daemon_instance_id，服务先检查实例再取消。CLI高级v2取消还先核对原Action/票据；旧Web和通用MCP cancel_job的ID取消不能误称为同等完整绑定。新Pattern v2 CLI/MCP取消使用原Ref/票据，见第12–13节。

同一实例内，原serial+键+相同规范内容在有界保留期内重放原任务，不再创建新任务；不同内容冲突。它不保证每条USB指令恰好一次，不跨重启保存，也不保证已接受任务一定成功。

### 一个可以照着走的HTTP例子

先把[呼吸作者文件](examples/api/breathe.action.json)保存为breathe.action.json，仅预览：

```sh
curl -sS -X POST http://127.0.0.1:8931/v1/light/preview \
  -H 'Content-Type: application/json' --data-binary @breathe.action.json
```

这一步不播放。需要实际播放时，先保存一次真实票据，再构造运行信封；不要把示意长ID写死：

```sh
nb1 control prepare > original-ticket.json
python3 - <<'PY'
import json
from pathlib import Path
intent = json.loads(Path('breathe.action.json').read_text())
ticket = json.loads(Path('original-ticket.json').read_text())
request = {**intent, 'policy': 'reject', **ticket}
Path('submit.json').write_text(json.dumps(request))
PY
# 下面一步才实际播放；20005601须换为本次选定设备。
curl -sS -D accepted.headers -o accepted.job.json \
  -X POST http://127.0.0.1:8931/v1/devices/20005601/light \
  -H 'Content-Type: application/json' --data-binary @submit.json
```

保存完整HTTP响应头和原票据，再按上表查原回执。上例是展示原理的最小curl流程；自动化调用还须核对状态、唯一实例/重放头、Job身份和进度，不能仅凭curl退出码宣布任务完成。Orbit作者文件改用v2预览/运行路径，不改变原文件或重复发送。

### 任务状态

```text
queued → running → succeeded
           │  └→ failed
           └→ cancelling → cancelled 或 failed
queued → cancelled
```

Program用completed_steps/total_steps；Frame用completed_frames/total_frames及completed_target_writes/planned_target_writes。计数满可能仍在等待尾部fade或清理。`cancelling`不代表已经停灯；`cancelled`表示worker已确认相应清理，失败则为failed。

### 返回对象字段速查

旧Program/light/Frame HTTP接受或查询返回Job本身；Pattern v2返回包含原Ref/执行摘要的PatternJobEnvelope。CLI结果通常放在job字段，MCP放在structuredContent.data；须按合同区分内外壳。

| 对象 | 字段 | 含义 |
| --- | --- | --- |
| DeviceState | serial、product、path?、firmware?、capabilities | 设备身份与缓存能力；path不是给调用方任意访问的文件路径API |
| DeviceState | connection、active_job?、queue_depth、last_error? | 连接/占用状态、当前任务和队列深度 |
| DeviceState | desired? | 最近逻辑目标Step，不是实时光学颜色 |
| DeviceState | observed? | led1、led2、sampled_at；可能是此前采样，不在每次GET现场访问USB |
| Job / FrameJob | id、device、status、program | 原任务身份、设备、状态与冻结执行内容 |
| Job | completed_steps、total_steps | 顺序步骤进度，包含repeat带来的总执行数量 |
| FrameJob | completed_frames、total_frames | 完整帧进度 |
| FrameJob | completed_target_writes、planned_target_writes | 目标设色进度；不能当作包含Read、Stop等命令的全部USB次数 |
| Job / FrameJob | created_at、started_at?、finished_at?、error? | RFC3339时间与错误；未发生的可选字段省略，不用null冒充 |

低层Program预览含schema_version、compile_contract、input_kind、program、nominal_duration_ms、total_steps、required_capabilities、validation和可选device。高级预览再由result提供规范action、编译合同与执行体；v2还含recipe_id和帧统计。`supported`或`cached_profile_supported`只是缓存匹配，不能替代排队成功、终态或摄像验收。

已成功hold的任务，再取消不会自动熄灯；需要另发明确off。客户端超时、MCP退出、网页关闭不自动取消daemon任务。同步原生调用也不能仅靠Go超时强行释放。

排队中的任务被取消时不会新增设备写入；其他活动任务或此前hold的颜色可以继续存在。因此不能把任意任务的cancelled状态直接等同于“整个设备已熄灭”。

### 未知结果的处理

断连、超时、响应身份错误、回执过期、实例变化，都不能自动换键再播放。保存原serial、Action/Program、票据和最后可信Job，只查原回执。404可能只是此刻没有回执，不能证明此前没有执行。完整性错误不能被随后一次正常响应掩盖。

纯预览失败不产生未知执行任务；保存的publication_unknown与播放的outcome_unknown也是两种不同结果。

## 11. 调度策略与当前不对称

| 策略 | 普通Program jobs | 当前light/Frame入口 | Pattern v2 HTTP（96a） |
| --- | --- | --- | --- |
| replace | 已实现；旧CLI默认 | 不接受 | 已实现，必须显式传入 |
| queue | 已实现，有界队列 | 不接受 | 已实现，必须显式传入 |
| reject | 已实现 | 必须显式使用，或由高级CLI/MCP明确选用 | 已实现，必须显式传入 |

这是当前真实状态，不是三套执行器。每设备仍只有一个worker，Program与Frame共享所有权。现有高级入口的策略限制尚未统一；不能在文档里先声称High/Low所有功能完全对等。 新Pattern HTTP与开发MCP run_pattern的policy必须显式传；新CLI patterns-v2 run的有效默认是reject，显式接受replace/queue/reject，不修改旧CLI父命令的replace默认。

## 12. 记忆与Pattern

Pattern v2当前P1规范要求所有层级的JSON对象键为ASCII，包括未知来源中的嵌套对象；Unicode字符串值（例如中文名称）可用。整数保留精确词法与int64范围，不应先经过JavaScript Number或通用float64对象再保存；未知来源不代表任意JSON都可进入库。

### 日常可用的Pattern v1

保存在电脑的有界create-only库，最多128条/1MiB。支持Program/旧Effect的作者包与冻结快照、list/get/import/export、固定id+revision预览/运行。导入不播放，不能覆盖同ID异内容，不用latest。

Web、CLI、HTTP、MCP已经接入；MCP只有list/get和固定引用预览/运行，没有导入工具。该库不能直接收新的FrameProgram，也不支持source.action。把v1 light预览结果的Program保存进去可以复用输出，但会丢高级作者来源；不要用它冒充完整高级Pattern保存。

### 开发已实现的Pattern v2

```json
{
  "id":"orbit-red-blue",
  "title":"红蓝环绕",
  "source":{
    "kind":"light_action",
    "contract":"nb1-light-action/experimental-v2",
    "action":{"kind":"orbit","colors":["red","blue"],"period_ms":3000,"cycles":2,"segments":4,"on_finish":"off"}
  }
}
```

预览生成完整Record：record_contract、id/title、source、compiled.normalized_action、可选recipe_id、typed execution和revision。Source保留颜色写法与省略默认值的JSON意义；不是逐字节保留原文件空白。

保存是在相同作者请求上加 `expected_revision`，确认完整候选后才一次Import。它不是覆盖权限或当前库版本，不需要daemon ID。新增返回201，同内容已存在200；不返回202或Job。

`execution_digest`只绑定冻结执行体；不同作者写法可以有相同执行摘要，但不同revision。revision确认完整记录，二者不能互相代替。未知且完整性合法的source/recipe可以只读展示/导出，不能新导入执行；库中存在未知项时整个库只读。

P3 提供作者 preview、条件 save、list/get、冻结快照 export/import；保存201/200与执行202分别处理。开发 daemon 已显式启用独立懒创建的 `UserConfigDir()/nb1/patterns-v2`，可用 `--pattern-v2-dir` / `NB1_PATTERN_V2_DIR` 选择；旧 `--pattern-dir` / `NB1_PATTERN_DIR` 只管 v1，自定义旧库路径不会隐式改选 v2。只构造 handler、纯作者预览与读取缺失库不创建目录；有效保存/导入进入写事务时才可能创建，后续发布失败也可能留下目录；既有库读取可能创建锁文件。路径配置错误不迁移、不回退、不覆盖旧库。Go handler 未注入 v2 option 时库操作仍501；配置错误503；纯作者 preview 始终零 Store。

P4 严格 typed 身份/摘要与原子准入、P5 完整来源 resolver、P6 公开引用执行均已在本册开发源码完成。resolver 复验 source、recipe、冻结执行和 revision，核心再校验 typed execution/digest/Ref，并与原票据原子绑定。不能把图案冻结体交给裸 Program 路由来冒充来源管理。

| P6 操作 | 输入与语义 |
| --- | --- |
| POST `/v2/patterns/preview-ref` | 完整 `pattern_ref`，可选 serial；复验已存来源后返回冻结 execution 与 Program/Frame 预览，选择设备时回显 serial/connection，只读一次缓存，不建 Job/票据。 |
| POST `/v2/devices/{serial}/pattern-jobs` | 完整 Ref、显式 replace/queue/reject、原 request_id/expected_daemon_instance_id；202 返回 PatternJobEnvelope，唯一 instance/replayed/Location。 |
| GET `/v2/devices/{serial}/pattern-requests/{request_id}` | 唯一四 query：expected_daemon_instance_id、record_contract、pattern_id、revision；200 原 Envelope，不依赖图案库。 |
| POST `/v2/devices/{serial}/pattern-requests/{request_id}/cancel` | Ref、expected_daemon_instance_id、可选 confirmed_job_id；省略才不加 ID 约束，空/null 非法；202 可仍 cancelling/terminal，不依赖库。 |

提交不能在核心回执查询前检查 Store。新 key 才调用惰性 verifier；精确旧回执在库丢失、配置失败或 Engine 关闭后仍可按核心规则重放/查询/取消。不要因库不可用就丢掉原票据，也不要自动换新票据重发。响应 Envelope 为 `{pattern_ref,execution_digest,job_kind,job}`；Job/FrameJob 本身未加字段，旧裸 Job getter 隐藏 Pattern 来源，不能退回历史猜结果。Location 已将完整 Ref 和原实例 URL 编码，可原样 GET。终态 hold 再取消不会关灯。

`pattern_execution.available` 只报告 PatternService 与 resolver 接线，检查状态零 Store 调用。true reason=`pattern_service_configured`；false reason=`pattern_service_unsupported`、`pattern_library_unconfigured` 或 `pattern_library_configuration_error`。true 不证明库健康、设备支持或本次准入成功；false 不阻断原回执恢复。Frame hold 的新 Pattern 准入额外要求缓存 hold，不能推广成旧 Frame helper 已做同样检查。

P6 新 POST 原始报文限4096字节，单个 application/json，无 query，严格 UTF8/duplicate/unknown/null，全部身份标量为闭合 ASCII；GET 四参数必须唯一、非空、无未知或空分隔段且无 body。revision/摘要语法不是来源验证，JSON Schema 无法证明哈希和票据绑定。历史1d89没有 P3/P6 新路由；历史f8bb HTTP基线不含这些客户端，96a整合候选已包含。CLI作者候选 `eee1eb952f01bab521d5514b74b60a5fa45d6e1d` 已有get/preview-draft/save；执行候选 `4eac5293bbad413e90ff19e6c8700628a1a4a8ee` 又新增preview/run/request/cancel（第13节），这些原片已整合并安装96a。CLI list/导入导出仍后续；96a已接高级图案书Web；MCP历史12和当前96a的16单独记录，HTTP能力不会自动扩展已安装工具。

固定引用预览可从实际 GET 记录提取 Ref；以下是已核实96a入口的只读示例，`PATTERN_ID` 必须是已保存记录。它不会播放：

```sh
NB1_ORIGIN=http://127.0.0.1:8931
PATTERN_ID=orbit-red-blue
record=$(curl --fail --silent "$NB1_ORIGIN/v2/patterns/$PATTERN_ID")
ref=$(printf '%s' "$record" | jq -c '.record | {record_contract,id,revision}')
jq -n --argjson pattern_ref "$ref" '{pattern_ref:$pattern_ref}' |
  curl --fail --silent -H 'Content-Type: application/json' --data-binary @- "$NB1_ORIGIN/v2/patterns/preview-ref"
```

运行仍需从真实 `/v1/instance` 准备一次原票据；保留完整 Ref/原票据/Location 后再按请求合同一次提交。网络失败或 context 结束不等于未接受，不能自动换票据补播。

电脑Pattern库、任务短期回执、设备RAM播放槽与硬件Flash是不同概念。当前记忆方案不默认自动重播，也不默认写硬件Flash；MK2并不妨碍在电脑保存高级图案。

## 13. CLI与MCP入口对照

本表包含旧入口与96a已安装的新入口。历史基线1d89是12工具；16工具原始schema与早期真实stdio软件证据固定 `c37f7fc33d52cc25619b10a5bc477965bf544cc0`；另有错误头校验补片 `679bdfc735a2f011c4c1fbf4fabec80f406f8f18`，工具形状不变。c37/679原片均未注册宿主；96a的真实stdio实机窗口另有上文记录，不能把c37软件证据改称96a或679实测。独立本地CLI作者候选 `eee1eb952f01bab521d5514b74b60a5fa45d6e1d`（历史f8bb HTTP资格之外，已整合96a）另提供 `control patterns-v2 get ID`、`control patterns-v2 preview-draft FILE`、`control patterns-v2 save FILE`。这三个作者命令仅daemon模式，保留完整作者来源，save输出原Ref和publication_confirmed/publication_unknown；不播放、不准备票据，无policy/wait。独立CLI执行候选 `4eac5293bbad413e90ff19e6c8700628a1a4a8ee` 另已完成下面四命令；list/导入导出仍在后续片，不与旧 `control patterns` 混用。

| 目的 | CLI | MCP |
| --- | --- | --- |
| 发现设备/准备票据 | control devices / control prepare | list_devices，可选prepare_request |
| 读设备缓存 | control state SERIAL | get_device_state |
| 归一化颜色 | control color INPUT / --file FILE | resolve_color |
| 预览高层动作 | control light preview FILE | preview_light |
| 播放高层动作 | control light run SERIAL FILE --wait | run_light |
| 原始Program/Effect预览 | control preview --program FILE / --effect FILE | preview_program |
| 原始Program播放 | control run SERIAL FILE | run_program |
| 旧Effect播放 | control effect SERIAL FILE | run_program的effect分支 |
| 原始单步设色 | control set SERIAL COLOR | set_device_state |
| 任务读取/停止 | control job ID / control cancel ID | get_job / cancel_job |
| 旧Pattern库 | control patterns list/get/import/export/preview/run | list_patterns/get_pattern；preview_program/run_program的pattern_ref分支 |

高级文件使用[示例目录](examples/api/)。在仓库/工作区外使用时，先把nb1替换为已核实的二进制路径；本机工作区当前为bin/nb1（固定96a原生字节）。

```sh
nb1 control --mode direct light preview breathe.action.json
nb1 control light preview breathe.action.json --serial 20005601
nb1 control light run 20005601 breathe.action.json --wait
```

前三条分别是纯本地预览、daemon缓存目标预览和实际播放；示例命令不会由文档自动执行。高级CLI可自动准备一次票据；需要事先留档时先control prepare，并传回原二元组。

MCP是**真实stdio协议服务**，经HTTP调用daemon，不执行CLI子进程、不直接打开USB。历史1d89的12个工具：cancel_job、get_device_state、get_job、get_pattern、list_devices、list_patterns、preview_light、preview_program、resolve_color、run_light、run_program、set_device_state。

当前96a的16工具完整输入/输出见[mcp-tools.schema.json](mcp-tools.schema.json)，来自干净候选二进制的实际tools/list；[历史1d89的12工具快照](mcp-tools.daily-1d89.schema.json)单独保留，旧12项schema相同。连接时以实际会话tools/list为准。调用示例（原有只预览）：

```json
{"name":"preview_light","arguments":{"contract":"nb1-light-action/v1","action":{"kind":"breathe","color":"lime","period_ms":2000,"cycles":2}}}
```

run_light必须有serial、原票据，仅reject。get_job按ID前缀区分旧/帧任务；只有票据时Frame必须显式job_kind:frame。MCP不自行展开动画，也不自动重发运行请求。

### CLI：高级Pattern七命令（96a已整合）

作者三个命令get/preview-draft/save不变，执行源码固定 `4eac5293bbad413e90ff19e6c8700628a1a4a8ee`。它沿P6 HTTP执行，已整合96a；原4e软件测试与96a有限实物窗口分别记录，工作区部署看第1节。

| 命令 | 字段、作用与边界 |
| --- | --- |
| `patterns-v2 get ID` | 完整已存Record；合法unknown仍只读。 |
| `patterns-v2 preview-draft FILE` | `{id,title,source}`作者纯预览，不保存、不发票据。 |
| `patterns-v2 save FILE` | 作者请求加expected_revision；输出原Ref和publication_confirmed/publication_unknown，不播放。 |
| `patterns-v2 preview ID --revision SHA [--serial SERIAL]` | 固定已存Ref预览；可选设备只读缓存，回显serial/connection，无票据/Job。 |
| `patterns-v2 run SERIAL ID --revision SHA` | 有效默认reject；显式--policy replace/queue/reject均可。无票据Prepare一次，有原二元组则零Prepare；单执行POST，无隐式库Get/preview。 |
| `patterns-v2 request SERIAL ID --revision SHA --request-id KEY --expected-daemon-instance-id INSTANCE` | 只读原回执；可加--job-id / --execution-digest约束已知身份，绝不读库或换票据。 |
| `patterns-v2 cancel SERIAL ID --revision SHA --request-id KEY --expected-daemon-instance-id INSTANCE` | 先GET原回执，terminal/cancelling零POST；其它最多一次带confirmed_job_id的取消。可加原Job/摘要约束。 |

SHA为真实记录的完整sha256:64位小写hex，不隐式选latest；record_contract固定实验v2并完整保留在输出。全部只走daemon，--mode direct拒绝。preview/作者命令不接受policy/wait/票据；request/cancel不接受policy且必须提供原票据。已安装96a未提供CLI list/import/export，不猜旧patterns命令会自动处理v2格式。

后续开发候选新增第八个 `control patterns-v2 list`（独立资格来源9ba82af，尚未推广）：只发一次GET列出高级库摘要，不逐项取Record、不预览/prepare/播放，也不打开返回路径。合法未知来源/配方照常显示，POSIX/Windows盘符/完整UNC按服务端路径语法解释。列表的verification是服务端广告；缺少完整Record时不能独立复算来源、revision和执行摘要，完整复验仍用get。current/Go1.18/race各两包783节点、167顶层通过；[列表候选的明确边界](../internal/daemonclient/README.pattern-list-v2.zh-CN.md)。快照导入导出CLI仍待补，当前本机命令仍按96a七项核对。

run/request/cancel支持--wait，受本次命令`--timeout`约束（默认30s，Prepare/POST/等待共用）；长图案应显式加长或稍后用原票据request恢复。等待只查询原Pattern回执，逐次核对完整typed Program/Frame、Ref、digest、Job ID、创建/启动时间、进度和生命周期；timeout不cancel、不重发、不用历史猜结果。已验证terminal先结束等待，随后caller context结束不把已知终态改成unknown。run/request等待到failed/cancelled可非零退出，仍输出可信终态且outcome_unknown=false；failed的optional错误文案为空也合法。

输出保留pattern_ref、request_ticket、accepted、outcome_unknown、daemon_instance_id、replayed及job。job是PatternJobEnvelope，内含原typed Job/FrameJob；accepted表示原任务已确认接受，不是完成。取消另外输出cancel_attempted，表示是否尝试过唯一POST，不能据此推断已送达或清理完成。

取消响应丢失、坏body/headers或身份退回时，保留原票据和最后可信before快照，立即返回错误；即使--wait也不自动用随后好GET掩盖坏响应。用户可之后明确request原票据。终态hold再次取消不自动关灯。

只有明确status+code组合可认定本次准入拒绝：400 invalid_pattern_request/invalid_request；404 pattern_not_found；409 busy/queue_full/idempotency_conflict/pattern_revision_conflict/pattern_library_busy；429 idempotency_capacity。普通404、未知code/错status、3xx、5xx/501、过期、实例变化与传输/解析失败都保留unknown，不授权生成新票据重试。查回执或已开始取消的错误不证明原任务未运行。

下例变量必须来自真实设备与保存记录；只展示命令，不由文档执行：

```sh
nb1 control patterns-v2 get orbit-red-blue
nb1 control patterns-v2 preview orbit-red-blue --revision "$REVISION" --serial "$SERIAL"
nb1 control patterns-v2 run "$SERIAL" orbit-red-blue --revision "$REVISION" --policy reject --wait
nb1 control patterns-v2 request "$SERIAL" orbit-red-blue --revision "$REVISION" --request-id "$REQUEST_ID" --expected-daemon-instance-id "$INSTANCE"
nb1 control patterns-v2 cancel "$SERIAL" orbit-red-blue --revision "$REVISION" --request-id "$REQUEST_ID" --expected-daemon-instance-id "$INSTANCE" --job-id "$JOB_ID" --execution-digest "$EXECUTION_DIGEST" --wait
```

客户端只复算返回的闭合执行体摘要与回执绑定，不在run前重建作者source；完整来源/recipe/revision验证仍由daemon P5+core负责。current/Go1.18七包各1818节点/411顶层、race四包1423/320通过；原回执GET同步caller停止场景重复20次通过。4e新增软件测试只用临时库、私有loopback与内存驱动；96a后续有限实机资格另列于第1节，不回写4e软件证据。详见[图案CLI分版说明](patterns-cli.md)、[内部执行客户端说明](../internal/daemonclient/README.pattern-execution-v2.zh-CN.md)。

### MCP：高级Pattern四工具（96a已整合）

完整来源与编译验证在daemon；MCP只验证闭合执行体、执行摘要与原Ref/票据/Job绑定，不复制高级编译器。新工具不会把Pattern降成旧Program任务。

| 工具 | 必填输入 | 行为 |
| --- | --- | --- |
| `preview_pattern` | 完整 `pattern_ref`，可选8位hex `serial` | 单次POST固定引用预览；不准备票据、不建任务，`accepted:false`。 |
| `run_pattern` | `pattern_ref,serial,policy,request_id,expected_daemon_instance_id` | policy显式replace/queue/reject；仅一次执行POST，**无隐式预览、库预查、Prepare或自动重试**。 |
| `get_pattern_request` | `pattern_ref,serial,request_id,expected_daemon_instance_id` | 只读原回执；可选已知`job_id`、`execution_digest`进一步约束。 |
| `cancel_pattern_request` | 同get，可选同样约束 | 先GET核对原任务，再最多一次带confirmed_job_id的取消POST；已终态或cancelling不再POST。 |

`pattern_ref`固定包含 `record_contract:"nb1-pattern-record/experimental-v2"`、ID与完整小写revision，必须来自原保存记录，不猜latest。运行前通过已有 `list_devices({prepare_request:true})` 取得一次原票据；重试与恢复保留原serial、Ref和二元组。库删除或当前不可读不应阻断已接收任务的原票据查停。新任务使用新Pattern工具，不能退回旧`get_job/cancel_job`剥离来源。

成功run的`structuredContent.kind`为`pattern_job`，`data`是完整`{pattern_ref,execution_digest,job_kind,job}`，外层保留`request_ticket`；接受不等于完成。query为`pattern_request`；cancel为`pattern_cancel`，其data含`pattern_job`（最后可信Envelope）与`cancel_attempted`。取消错误仍保留原票据及最后可信快照；`cancel_attempted`只说明是否尝试过唯一一次POST，不证明已熄灯。坏响应、断连、实例变化和回执过期不能触发换键补播；完整性错误不会被后续看似正确的响应掩盖。

c37历史片current/race各816测试节点通过、3项显式live跳过；真实stdio→P6 HTTP→临时StoreV2→内存fake Engine贯通，未访问USB。679仅增加Pattern错误响应头检查，独立软件验证另有归档；此处工具快照/stdio证据仍绑定c37。独立MCP模块沿用Go1.25下限，不能以root Go1.18兼容性替代。日常96a未提供v2库作者工具；后续MCP20候选见本节。导入/导出MCP仍未提供，原list_patterns/get_pattern保持v1语义。

### 后续候选MCP20：主机高级库作者入口（未安装）

开发整合另包含固定来源10cf37f的四个作者工具，旧16完整schema不变。`list_patterns_v2`读取高级摘要；`get_pattern_v2`读取完整Record；`preview_pattern_draft_v2`编译作者草稿；`save_pattern_v2`按expected_revision条件保存。它们不prepare运行票据、不创建Job或播放，不改旧list_patterns/get_pattern的v1含义。实际schema见[候选20工具快照](mcp-tools.candidate-20.schema.json)，本机日常96a仍为16工具。

完整记录用record_json字符串传递，保留精确整数；保存不确定时保留original_author_json和原Ref，仅显式GET核对。主机库create-only，修改周期后不能覆盖原ID，应保存为新ID；保留可编辑来源不等于已经有同名版本树。MCP由daemon权威编译，适配器验证Record revision/typed执行摘要，不能称为本地语义复编译。4MiB预算只含适配器构造的CallToolResult，不含SDK自动_meta或JSON-RPC外封套。

固定源current/Go1.25/race各1016节点117顶层通过，3项live跳过；fresh真实stdio→HTTP→临时Store有22请求软件证据，未用USB。候选整合另补MCP race的8分钟有界CI预算；未跑本片hosted矩阵或自动注册宿主。

## 14. 完整HTTP索引与错误

以下索引逐项来自路由实现，包含运行、只读计算、存储和静态资源。`/v2`并非所有接口整体升级：light v2是实验Orbit；patterns v2是高级来源记录与固定引用执行语义；旧路径与合同继续保留。

路径中的`{id}`、`{jobId}`是占位变量名；同一位置表示同一个任务ID，不是两条不同路由。OpenAPI另显式列出GET普通ID为preview/import/preview-ref时的四个路径别名，不代表新增能力。


Web宿主准备源码固定 `56c4aef153c560c68845e89ebdaa15bc1323eeb2`，保持两种完整、闭合的产物：基础含6个公开页面资源+LICENSE/2合同，扩展含9个公开页面资源+LICENSE/4合同，manifest均另计，总dist≤256KiB。GET /demo重定向另算一路。因此源码索引增加3个静态路径，不代表新增硬件动作。历史56c固定旧Web fe32507，新三路径实际404，原记录保留。96a已导入Web `f90ef8d6143acd55c059b94ecf961ec65a1bb646`：10项manifest.files（LICENSE+9公开资源）加manifest共208132字节、4合同，manifest SHA256 `9f9c039abd50c65bf8ebc2e2a74954092e7cd0fff0871021666028adc9a2519d`；三路径在96a实际返回200并与固定dist逐字节一致。合法基础bundle仍可恢复且新三路径404；新dist导入与实际安装是两步；本次已按第1节单独推广。详细边界见[Web工作台](web-debugger.zh-CN.md)。

所有路径先经过 loopback Host/同源 Origin，返回 no-store、nosniff；未开放 CORS、公网鉴权或云控端点。下列错误引用同名分组，详见文末。

### GET /healthz

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** none。Query：none in documented canonical use。

**成功：** 200；{status:"ok",api_version:"v1"}。

**I/O / 执行：** No service/USB call; proves HTTP handler only.

**票据：** none

**错误分组/代码：** global。

来源：[internal/httpapi/httpapi.go](../internal/httpapi/httpapi.go)；测试：[httpapi_test.go](../internal/httpapi/httpapi_test.go)。

### GET /v1/capabilities

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** none。Query：RawQuery must be empty。

**成功：** 200；control.CapabilityCatalog; compile_contract, supported_inputs, effects, policies, color_model, led_selectors, program/effect limits, pattern_library, hardware_pattern_upload。

**I/O / 执行：** No USB; if v1 Store configured, calls Store.List to report pattern_library, so not zero filesystem I/O.

**票据：** none

**错误分组/代码：** global, invalid_request。

**边界：** Old catalog describes Program/Effect and optionally v1 pattern_ref; use frame-capabilities separately. hardware_pattern_upload=false.

来源：[internal/httpapi/preview.go](../internal/httpapi/preview.go)；测试：[preview_test.go](../internal/httpapi/preview_test.go)、[patterns_test.go](../internal/httpapi/patterns_test.go)。

### GET /v1/instance

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** none。Query：none in documented canonical use。

**成功：** 200；InstanceInfo{daemon_instance_id,idempotency{supported,request_id_prefix,accept_until_uptime_ms,max_accept_window_ms,receipt_retention_ms,max_entries,max_retained_steps}}。

**I/O / 执行：** No USB; computes uptime window; does not reserve receipt capacity or submit Job.

**票据：** Returns preparation metadata; caller appends 16 random bytes as 32 lowerhex chars to request_id_prefix.

**错误分组/代码：** global, idempotency_unsupported。

**成功头：** X-NB1-Daemon-Instance-ID。

来源：[internal/httpapi/httpapi.go](../internal/httpapi/httpapi.go)；测试：[receipt_test.go](../internal/httpapi/receipt_test.go)。

### GET /v1/transport-metrics

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** none。Query：none in documented canonical use。

**成功：** 200；[]TransportMetric{serial,operation,calls,attempts,retries,failures,last_error?}。

**I/O / 执行：** Only copied in-memory state/counters; handler does not request a USB sample. Background worker monitoring is separate.

**票据：** none

**错误分组/代码：** global。

**边界：** Missing optional Metrics provider returns []. LastError is historical; retries may have recovered.

来源：[internal/httpapi/httpapi.go](../internal/httpapi/httpapi.go)；测试：[httpapi_test.go](../internal/httpapi/httpapi_test.go)。

### GET /v1/devices

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** none。Query：none in documented canonical use。

**成功：** 200；[]DeviceState (empty is [])。

**I/O / 执行：** Only copied in-memory state/counters; handler does not request a USB sample. Background worker monitoring is separate.

**票据：** none

**错误分组/代码：** global。

来源：[internal/httpapi/httpapi.go](../internal/httpapi/httpapi.go)；测试：[httpapi_test.go](../internal/httpapi/httpapi_test.go)。

### GET /v1/devices/{serial}

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** none。Query：none in documented canonical use。

**成功：** 200；DeviceState; serial/product/path?/firmware?/capabilities,connection,active_job?,queue_depth,desired?,observed?,last_error?。

**I/O / 执行：** Only copied in-memory state/counters; handler does not request a USB sample. Background worker monitoring is separate.

**票据：** none

**错误分组/代码：** global, service。

**边界：** This is CLI control state route. No /state suffix; observed.sampled_at must be retained. desired is a target, not observation.

来源：[internal/httpapi/httpapi.go](../internal/httpapi/httpapi.go)；测试：[httpapi_test.go](../internal/httpapi/httpapi_test.go)、[preview_test.go](../internal/httpapi/preview_test.go)。

### POST /v1/programs/preview

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** Exactly one of program:Program / effect:Effect / pattern_ref:{id,revision}; optional serial。Query：RawQuery must be empty。 上限 1048576B。

**成功：** 200；Program/Effect: PreviewResult{schema_version,compile_contract,input_kind,program,nominal_duration_ms,total_steps,required_capabilities,validation,device?}; reference: {pattern_ref,preview}。

**I/O / 执行：** Raw Program/Effect pure; optional serial reads one cached snapshot only. pattern_ref reads/resolves v1 Store (filesystem I/O), never submits/opens USB.

**票据：** No policy/ticket/dry_run allowed. Does not prepare receipt.

**错误分组/代码：** global, json_1m, service, library。

来源：[internal/httpapi/preview.go](../internal/httpapi/preview.go)；测试：[preview_test.go](../internal/httpapi/preview_test.go)、[patterns_test.go](../internal/httpapi/patterns_test.go)。

### POST /v1/devices/{serial}/jobs

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** Exactly one program:Program / effect:Effect / pattern_ref:{id,revision}, policy; request_id and expected_daemon_instance_id both present or both absent for raw Program/Effect。Query：Canonical use has none; legacy raw branch does not explicitly reject all query/body extras beyond strict JSON schema。 上限 1048576B。

**成功：** 202；Job (bare, not receipt wrapper)。

**I/O / 执行：** Queues same device worker; actual execution calls Stop(hold), Set/waits, final Stop(off|hold)+Read.

**票据：** Raw Program/Effect supports legacy unkeyed mode; supplied pair uses atomic receipt. v1 pattern_ref requires explicit reject + original ticket.

**错误分组/代码：** global, json_1m, service, library。

**成功头：** keyed: X-NB1-Daemon-Instance-ID；keyed: X-NB1-Replayed true|false；Location: keyed original request URI; unkeyed /v1/jobs/{id}。

**边界：** Raw policy empty defaults replace; accepts replace/queue/reject. New callers should explicitly choose policy and ticket. CLI control set is a one-Step Program with on_finish:hold sent here; there is no separate /set HTTP route. v1 Pattern returns frozen Program Job and does not retain v2 provenance.

来源：[internal/httpapi/httpapi.go](../internal/httpapi/httpapi.go)；测试：[receipt_test.go](../internal/httpapi/receipt_test.go)、[effects_test.go](../internal/httpapi/effects_test.go)、[patterns_execution_test.go](../internal/httpapi/patterns_execution_test.go)。

### GET /v1/devices/{serial}/requests/{request_id}

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** documented none。Query：Exactly one nonempty expected_daemon_instance_id。

**成功：** 200；Original current Job snapshot。

**I/O / 执行：** Only copied in-memory state/counters; handler does not request a USB sample. Background worker monitoring is separate. Lookup can prune expired terminal receipts.

**票据：** Original serial+request_id+instance; no Store lookup.

**错误分组/代码：** global, service。

**成功头：** X-NB1-Daemon-Instance-ID。

**边界：** 404 request_not_found does not prove original POST never arrived.

来源：[internal/httpapi/httpapi.go](../internal/httpapi/httpapi.go)；测试：[receipt_test.go](../internal/httpapi/receipt_test.go)、[patterns_execution_test.go](../internal/httpapi/patterns_execution_test.go)。

### GET /v1/jobs/{id}

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** none。Query：none in documented canonical use。

**成功：** 200；Job。

**I/O / 执行：** Only copied in-memory state/counters; handler does not request a USB sample. Background worker monitoring is separate.

**票据：** ID only; bounded ordinary history, separate from receipt retention.

**错误分组/代码：** global, service。

来源：[internal/httpapi/httpapi.go](../internal/httpapi/httpapi.go)；测试：[httpapi_test.go](../internal/httpapi/httpapi_test.go)、[receipt_test.go](../internal/httpapi/receipt_test.go)。

### POST /v1/jobs/{id}/cancel

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** empty or {} only; no source/instance/ticket fields。Query：canonical none; old branch does not explicitly reject all query parameters。

**成功：** 202；Current Job, may be cancelling/queued-cancelled/already-terminal。

**I/O / 执行：** Mutates cancellation state; worker confirms cleanup before active task terminal. Does not itself interrupt native I/O.

**票据：** ID only; no original instance precondition on this legacy route.

**错误分组/代码：** global, json_1m, service。

**边界：** Cancelling is not cleanup complete; cancelling succeeded hold returns terminal job without extinguishing held output.

来源：[internal/httpapi/httpapi.go](../internal/httpapi/httpapi.go)；测试：[httpapi_test.go](../internal/httpapi/httpapi_test.go)、[engine_test.go](../internal/control/engine_test.go)。

### POST /v1/colors/resolve

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** {color:ColorInput}。Query：none, including empty ?。 上限 4096B。

**成功：** 200；{schema_version:1,color_contract:"nb1-color-input/v1",input_kind,canonical_color,rgb8:{r,g,b},palette?}。

**I/O / 执行：** Pure bounded computation; no device discovery/open/Set/Read, no Job/receipt preparation.

**票据：** none

**错误分组/代码：** global, 405 method_not_allowed, 415 json_required, 413 body_too_large, 400 invalid_request(query), 400 invalid_color。

**边界：** No accepted:false or Job field in HTTP response; MCP wrapper is separate.

来源：[internal/httpapi/color.go](../internal/httpapi/color.go)；测试：[color_test.go](../internal/httpapi/color_test.go)。

### GET /v1/frame-capabilities

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** none。Query：none including empty ?。

**成功：** 200；schema_version:1,contract,experimental:true,software_supported,max_frames,max_executed_frames,max_target_writes,min/max_interval_ms,fade_quantum_ms,write_spacing_ms,max_nominal_duration_ms,policy:"reject",initialization,timing_basis,required_device_capabilities。

**I/O / 执行：** No USB, type-asserts FrameService only.

**票据：** none

**错误分组/代码：** global, invalid_request。

来源：[internal/httpapi/frames.go](../internal/httpapi/frames.go)；测试：[frames_test.go](../internal/httpapi/frames_test.go)。

### POST /v1/frame-programs/preview

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** {program:FrameProgram,serial?:string}。Query：none including empty ?。 上限 1048576B。

**成功：** 200；FramePreview + device?:{serial,connection}。

**I/O / 执行：** Pure schedule preview; serial checks only configured cached snapshot. No FrameService still returns valid preview with execution unavailable.

**票据：** none

**错误分组/代码：** global, json_1m, service。

**边界：** execution unavailable reason distinguishes not_selected/frame_service_unsupported/device_not_configured/device_not_online/missing_capabilities; no fallback to steps.

来源：[internal/httpapi/frames.go](../internal/httpapi/frames.go)；测试：[frames_test.go](../internal/httpapi/frames_test.go)。

### POST /v1/devices/{serial}/frame-jobs

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** {program:FrameProgram,policy:"reject",request_id,expected_daemon_instance_id}。Query：none including empty ?。 上限 1048576B。

**成功：** 202；FrameJob。

**I/O / 执行：** Same worker/lease; paired target writes 10ms apart, host intervals, final fade wait and shared cleanup. No periodic Read during frame schedule.

**票据：** Mandatory complete original pair; no unkeyed/replace/queue raw Frame route.

**错误分组/代码：** global, json_1m, service, 501 frame_unsupported。

**成功头：** X-NB1-Replayed；X-NB1-Daemon-Instance-ID；Location: /v1/devices/{serial}/frame-requests/{request_id}?expected_daemon_instance_id=...。

来源：[internal/httpapi/frames.go](../internal/httpapi/frames.go)；测试：[frames_test.go](../internal/httpapi/frames_test.go)、[frames_engine_test.go](../internal/httpapi/frames_engine_test.go)。

### GET /v1/devices/{serial}/frame-requests/{request_id}

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** none。Query：Exactly one nonempty expected_daemon_instance_id。

**成功：** 200；Original FrameJob。

**I/O / 执行：** Only copied in-memory state/counters; handler does not request a USB sample. Background worker monitoring is separate. No Store.

**票据：** Original frame receipt; Program receipt route does not substitute.

**错误分组/代码：** global, service, 501 frame_unsupported。

**成功头：** X-NB1-Daemon-Instance-ID。

来源：[internal/httpapi/frames.go](../internal/httpapi/frames.go)；测试：[frames_test.go](../internal/httpapi/frames_test.go)。

### GET /v1/frame-jobs/{id}

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** none。Query：none including empty ?。

**成功：** 200；FrameJob。

**I/O / 执行：** Only copied in-memory state/counters; handler does not request a USB sample. Background worker monitoring is separate.

**票据：** ID only history.

**错误分组/代码：** global, service, 501 frame_unsupported。

来源：[internal/httpapi/frames.go](../internal/httpapi/frames.go)；测试：[frames_test.go](../internal/httpapi/frames_test.go)。

### POST /v1/frame-jobs/{id}/cancel

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** empty / {} legacy; optional nonempty expected_daemon_instance_id binds instance; explicit null/blank/wrong type/unknown/duplicate fields rejected。Query：none including empty ?。

**成功：** 202；Current FrameJob; cancelling is possible。

**I/O / 执行：** Checks supplied immutable instance before CancelFrame; only same worker performs Stop/Read.

**票据：** ID only legacy remains; optional instance is not full Action/ticket binding.

**错误分组/代码：** global, json_1m, service, 501 frame_unsupported。

**成功头：** X-NB1-Daemon-Instance-ID only when conditional instance branch succeeds。

**边界：** CLI high v2 cancel first validates original Action+receipt, then uses this conditional cancel once; no dedicated HTTP action-cancel route. Current Web/MCP generic cancel uses existing ID-only branch; do not claim equal cross-instance protection.

来源：[internal/httpapi/frames.go](../internal/httpapi/frames.go)；测试：[frame_cancel_bound_test.go](../internal/httpapi/frame_cancel_bound_test.go)、[frames_test.go](../internal/httpapi/frames_test.go)。

### POST /v1/light/preview

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** {contract:"nb1-light-action/v1",action:{kind:set/off/fade/blink/breathe/pulse/alternate/rainbow,...},serial?:string}。Query：none including empty ?。 上限 65536B。

**成功：** 200；{result: normalized Action + frozen Program/FrameProgram and compile facts, preview: matching low preview}。

**I/O / 执行：** No Job/ticket/USB. Optional serial checks one cached device snapshot only.

**票据：** No policy/dry_run/ticket accepted.

**错误分组/代码：** global, json_64k, 400 invalid_light_action, service。

来源：[internal/httpapi/light.go](../internal/httpapi/light.go)；测试：[light_test.go](../internal/httpapi/light_test.go)。

### POST /v1/devices/{serial}/light

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** {contract:"nb1-light-action/v1",action:{...},policy:"reject",request_id,expected_daemon_instance_id}。Query：none including empty ?。 上限 65536B。

**成功：** 202；Job (bare)。

**I/O / 执行：** Original high intent compiled by nb1; same low worker admission/executor.

**票据：** Mandatory original pair; explicit reject only; no automatic POST replay in provided high clients.

**错误分组/代码：** global, json_64k, 400 invalid_light_action, service, 501 idempotency_unsupported。

**成功头：** X-NB1-Replayed；X-NB1-Daemon-Instance-ID；Location: corresponding original Program/frame receipt。

**边界：** v1 old alternate.transition=orbit remains old sequential Program, not experimental-v2 orbit.

来源：[internal/httpapi/light.go](../internal/httpapi/light.go)；测试：[light_test.go](../internal/httpapi/light_test.go)。

### POST /v2/light/preview

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** {contract:"nb1-light-action/experimental-v2",action:{kind:orbit only,...},serial?:string}。Query：none including empty ?。 上限 65536B。

**成功：** 200；{result: normalized Action + frozen Program/FrameProgram and compile facts, preview: matching low preview}。

**I/O / 执行：** No Job/ticket/USB. Optional serial checks one cached device snapshot only.

**票据：** No policy/dry_run/ticket accepted.

**错误分组/代码：** global, json_64k, 400 invalid_light_action, service。

来源：[internal/httpapi/light_v2.go](../internal/httpapi/light_v2.go)；测试：[frames_test.go](../internal/httpapi/frames_test.go)、[frames_engine_test.go](../internal/httpapi/frames_engine_test.go)。

### POST /v2/devices/{serial}/light

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** {contract:"nb1-light-action/experimental-v2",action:{...},policy:"reject",request_id,expected_daemon_instance_id}。Query：none including empty ?。 上限 65536B。

**成功：** 202；FrameJob (bare)。

**I/O / 执行：** Original high intent compiled by nb1; same low worker admission/executor.

**票据：** Mandatory original pair; explicit reject only; no automatic POST replay in provided high clients.

**错误分组/代码：** global, json_64k, 400 invalid_light_action, service, 501 frame_unsupported。

**成功头：** X-NB1-Replayed；X-NB1-Daemon-Instance-ID；Location: corresponding original Program/frame receipt。

**边界：** 4/8 segments; preserves high Action authoring; no hand-authored frames required.

来源：[internal/httpapi/light_v2.go](../internal/httpapi/light_v2.go)；测试：[frames_test.go](../internal/httpapi/frames_test.go)、[frames_engine_test.go](../internal/httpapi/frames_engine_test.go)。

### GET /v1/patterns

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** none。Query：none or exactly format=snapshot。

**成功：** 200；PatternList{schema_version:1,contract,library,patterns}; format=snapshot canonical v1 Snapshot bytes。

**I/O / 执行：** Filesystem Store read only; no device/service method, Job or execution ticket.

**票据：** none

**错误分组/代码：** global, library。

**边界：** configured v1 library selection only; does not expose v2 records.

来源：[internal/httpapi/patterns.go](../internal/httpapi/patterns.go)；测试：[patterns_test.go](../internal/httpapi/patterns_test.go)。

### GET /v1/patterns/{id}

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** none。Query：none or exactly format=snapshot; IDs ASCII1–64。

**成功：** 200；Frozen v1 Record; format=snapshot single canonical Snapshot。

**I/O / 执行：** Filesystem Store read only; no device/service method, Job or execution ticket.

**票据：** none

**错误分组/代码：** global, library。

**边界：** GET import is a legal ID.

来源：[internal/httpapi/patterns.go](../internal/httpapi/patterns.go)；测试：[patterns_test.go](../internal/httpapi/patterns_test.go)。

### POST /v1/patterns/import

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** v1 author package or frozen snapshot accepted by Store.Import; not advanced Action source。Query：none including empty ?。 上限 1048576B。

**成功：** 200；ImportResult{records,created,existing,total}。

**I/O / 执行：** Filesystem create-only atomic import; no device/service method or execution.

**票据：** No execution receipt. publication_unknown is storage uncertainty, not Job uncertainty.

**错误分组/代码：** global, 415 json_required, 413 pattern_package_too_large, library。

来源：[internal/httpapi/patterns.go](../internal/httpapi/patterns.go)；测试：[patterns_test.go](../internal/httpapi/patterns_test.go)。

### GET /v2/patterns

**版本：** 历史f8bb267已实现、1d89无；96a已整合，实际部署看第1节。

**输入：** none。Query：none or exactly format=snapshot。

**成功：** 200；v2 List envelope {schema_version:2,contract,library,pattern_execution,patterns}; format=snapshot canonical v2 Snapshot bytes。

**I/O / 执行：** Filesystem Store read only; no device/service method, Job or execution ticket.

**票据：** none

**错误分组/代码：** global, library, library_v2。

**边界：** pattern_execution is wiring only; true pattern_service_configured; false service_unsupported/library_unconfigured/library_configuration_error with pattern_ prefixes; zero Store call to report status.

来源：[internal/httpapi/patterns_v2.go](../internal/httpapi/patterns_v2.go)；测试：[patterns_v2_test.go](../internal/httpapi/patterns_v2_test.go)。

### GET /v2/patterns/{id}

**版本：** 历史f8bb267已实现、1d89无；96a已整合，实际部署看第1节。

**输入：** none。Query：none or exactly format=snapshot; IDs ASCII1–64。

**成功：** 200；v2 {schema_version:2,contract,record,verification,execution_digest}; format=snapshot single canonical Snapshot。

**I/O / 执行：** Filesystem Store read only; no device/service method, Job or execution ticket.

**票据：** none

**错误分组/代码：** global, library, library_v2。

**边界：** GET preview/import/preview-ref/export/capabilities are ordinary IDs, not operation routes. Unknown source/recipe is explicit unverified read-only data, not executable. pattern_execution is wiring only; true pattern_service_configured; false service_unsupported/library_unconfigured/library_configuration_error with pattern_ prefixes; zero Store call to report status.

来源：[internal/httpapi/patterns_v2.go](../internal/httpapi/patterns_v2.go)；测试：[patterns_v2_test.go](../internal/httpapi/patterns_v2_test.go)。

### POST /v2/patterns/import

**版本：** 历史f8bb267已实现、1d89无；96a已整合，实际部署看第1节。

**输入：** Full verified canonical-compatible v2 frozen Snapshot; no authoring body。Query：none including empty ?。 上限 1048576B。

**成功：** 200；{schema_version:2,contract,records,created,existing,total}。

**I/O / 执行：** Filesystem create-only atomic import; no device/service method or execution.

**票据：** No execution receipt. publication_unknown is storage uncertainty, not Job uncertainty.

**错误分组/代码：** global, 415 json_required, 413 pattern_package_too_large, library, library_v2。

**边界：** pattern_execution is wiring only; true pattern_service_configured; false service_unsupported/library_unconfigured/library_configuration_error with pattern_ prefixes; zero Store call to report status.

来源：[internal/httpapi/patterns_v2.go](../internal/httpapi/patterns_v2.go)；测试：[patterns_v2_test.go](../internal/httpapi/patterns_v2_test.go)。

### POST /v2/patterns/preview

**版本：** 历史f8bb267已实现、1d89无；96a已整合，实际部署看第1节。

**输入：** {id,title,source:{kind:"light_action",contract,action}}; no serial/policy/ticket/expected_revision/program。Query：none including empty ?。 上限 1048576B。

**成功：** 200；{schema_version:2,contract,record,verification,execution_digest,preview,pattern_execution:{available,reason}}。

**I/O / 执行：** Pure codec only; Store zero calls including Path; Service zero calls; no USB.

**票据：** none

**错误分组/代码：** global, 415 json_required, 413 pattern_package_too_large, library_v2。

**边界：** pattern_execution is wiring only; true pattern_service_configured; false service_unsupported/library_unconfigured/library_configuration_error with pattern_ prefixes; zero Store call to report status.

来源：[internal/httpapi/patterns_v2.go](../internal/httpapi/patterns_v2.go)；测试：[patterns_v2_test.go](../internal/httpapi/patterns_v2_test.go)。

### POST /v2/patterns

**版本：** 历史f8bb267已实现、1d89无；96a已整合，实际部署看第1节。

**输入：** Same author source + expected_revision matching full freshly built candidate; create-only。Query：none including empty ?。 上限 1048576B。

**成功：** 201/200；{schema_version:2,contract,records,created,existing,total}; 201 new ID,200 identical existing。

**I/O / 执行：** Rebuild/verify pure candidate, then exactly one Store.Import; no execution/service/USB.

**票据：** No daemon/request ticket; expected_revision confirms candidate, not overwrite permission.

**错误分组/代码：** global, 415 json_required, 413 pattern_package_too_large, library, library_v2。

**成功头：** Location: /v2/patterns/{id}。

**边界：** pattern_execution is wiring only; true pattern_service_configured; false service_unsupported/library_unconfigured/library_configuration_error with pattern_ prefixes; zero Store call to report status.

来源：[internal/httpapi/patterns_v2.go](../internal/httpapi/patterns_v2.go)；测试：[patterns_v2_test.go](../internal/httpapi/patterns_v2_test.go)。

### POST /v2/patterns/preview-ref

**版本：** 历史f8bb267已实现、1d89无；96a已整合，实际部署看第1节。

**输入：** {pattern_ref,serial?}。Query：none including empty ?。 上限 4096B。

**成功：** 200；PatternRefPreviewV2。

**I/O / 执行：** P5 exact Resolve and verification, pure Program/Frame facts; serial optionally one cached Device; no Job/receipt/USB.

**票据：** none

**错误分组/代码：** global, pattern_execution_v2, library_v2。

**边界：** Resolve once; no Path/other Store call. Strict raw POST4096B, valid UTF8, all fields closed ASCII; duplicate/unknown/null/ForceQuery rejected.

来源：[internal/httpapi/pattern_execution_v2.go](../internal/httpapi/pattern_execution_v2.go)；测试：[pattern_execution_v2_test.go](../internal/httpapi/pattern_execution_v2_test.go)、[pattern_execution_v2_engine_test.go](../internal/httpapi/pattern_execution_v2_engine_test.go)。

### POST /v2/devices/{serial}/pattern-jobs

**版本：** 历史f8bb267已实现、1d89无；96a已整合，实际部署看第1节。

**输入：** {pattern_ref,policy,request_id,expected_daemon_instance_id}。Query：none including empty ?。 上限 4096B。

**成功：** 202；PatternJobEnvelopeV2。

**I/O / 执行：** Core new-receipt lazy Resolve then source-bound admission; accepted replay skips Store/config; worker uses existing execution.

**票据：** Original serial/request_id/instance + full Ref + explicit replace/queue/reject.

**错误分组/代码：** global, pattern_execution_v2。

**成功头：** X-NB1-Daemon-Instance-ID: exactly one original instance；X-NB1-Replayed: exactly one true|false；Location: original Pattern receipt path + four URL encoded query fields。

**边界：** No Store precheck; no client Program/Action/digest. Strict raw POST4096B, valid UTF8, all fields closed ASCII; duplicate/unknown/null/ForceQuery rejected.

来源：[internal/httpapi/pattern_execution_v2.go](../internal/httpapi/pattern_execution_v2.go)；测试：[pattern_execution_v2_test.go](../internal/httpapi/pattern_execution_v2_test.go)、[pattern_execution_v2_engine_test.go](../internal/httpapi/pattern_execution_v2_engine_test.go)。

### GET /v2/devices/{serial}/pattern-requests/{request_id}

**版本：** 历史f8bb267已实现、1d89无；96a已整合，实际部署看第1节。

**输入：** none。Query：exactly expected_daemon_instance_id,record_contract,pattern_id,revision, once each nonempty。

**成功：** 200；PatternJobEnvelopeV2。

**I/O / 执行：** Core original receipt lookup only; zero Store/config/USB.

**票据：** Original path request_id + four exact scalar query fields.

**错误分组/代码：** global, pattern_execution_v2。

**成功头：** X-NB1-Daemon-Instance-ID: exactly one original instance。

**边界：** No history/legacy Job fallback; retained terminal receipts remain queryable after Engine close. Strict raw POST4096B, valid UTF8, all fields closed ASCII; duplicate/unknown/null/ForceQuery rejected.

来源：[internal/httpapi/pattern_execution_v2.go](../internal/httpapi/pattern_execution_v2.go)；测试：[pattern_execution_v2_test.go](../internal/httpapi/pattern_execution_v2_test.go)、[pattern_execution_v2_engine_test.go](../internal/httpapi/pattern_execution_v2_engine_test.go)。

### POST /v2/devices/{serial}/pattern-requests/{request_id}/cancel

**版本：** 历史f8bb267已实现、1d89无；96a已整合，实际部署看第1节。

**输入：** {pattern_ref,expected_daemon_instance_id,confirmed_job_id?}。Query：none including empty ?。 上限 4096B。

**成功：** 202；PatternJobEnvelopeV2。

**I/O / 执行：** Core bound cancel; zero Store/config lookup. Active cleanup is existing worker behavior.

**票据：** Original request_id/path, instance, full Ref; optional confirmed Job ID.

**错误分组/代码：** global, pattern_execution_v2。

**成功头：** X-NB1-Daemon-Instance-ID: exactly one original instance。

**边界：** 202 may still cancelling/terminal; terminal hold not turned off; no implicit GET. Strict raw POST4096B, valid UTF8, all fields closed ASCII; duplicate/unknown/null/ForceQuery rejected.

来源：[internal/httpapi/pattern_execution_v2.go](../internal/httpapi/pattern_execution_v2.go)；测试：[pattern_execution_v2_test.go](../internal/httpapi/pattern_execution_v2_test.go)、[pattern_execution_v2_engine_test.go](../internal/httpapi/pattern_execution_v2_engine_test.go)。

### GET /demo

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** none。Query：none including empty ?。

**成功：** 307；307 redirect to /demo/。

**I/O / 执行：** Read embedded static bytes only; page load itself creates no light Job; subsequent explicit user actions call documented APIs.

**票据：** none

**错误分组/代码：** global, invalid_request, 404 not_found, 500 internal_error。

来源：[internal/httpapi/light.go](../internal/httpapi/light.go)；测试：[light_test.go](../internal/httpapi/light_test.go)。

### GET /demo/

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** none。Query：none including empty ?。

**成功：** 200；HTML index。

**I/O / 执行：** Read embedded static bytes only; page load itself creates no light Job; subsequent explicit user actions call documented APIs.

**票据：** none

**错误分组/代码：** global, invalid_request, 404 not_found, 500 internal_error。

**成功头：** Content-Security-Policy: default-src none; script/style/connect self; base/frame/form none；Referrer-Policy: no-referrer。

来源：[internal/httpapi/light.go](../internal/httpapi/light.go)；测试：[light_test.go](../internal/httpapi/light_test.go)。

### GET /demo/demo.js

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** none。Query：none including empty ?。

**成功：** 200；JS。

**I/O / 执行：** Read embedded static bytes only; page load itself creates no light Job; subsequent explicit user actions call documented APIs.

**票据：** none

**错误分组/代码：** global, invalid_request, 404 not_found, 500 internal_error。

**成功头：** Content-Security-Policy: default-src none; script/style/connect self; base/frame/form none；Referrer-Policy: no-referrer。

来源：[internal/httpapi/light.go](../internal/httpapi/light.go)；测试：[light_test.go](../internal/httpapi/light_test.go)。

### GET /demo/demo.css

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** none。Query：none including empty ?。

**成功：** 200；CSS。

**I/O / 执行：** Read embedded static bytes only; page load itself creates no light Job; subsequent explicit user actions call documented APIs.

**票据：** none

**错误分组/代码：** global, invalid_request, 404 not_found, 500 internal_error。

**成功头：** Content-Security-Policy: default-src none; script/style/connect self; base/frame/form none；Referrer-Policy: no-referrer。

来源：[internal/httpapi/light.go](../internal/httpapi/light.go)；测试：[light_test.go](../internal/httpapi/light_test.go)。

### GET /demo/orbit.html

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** none。Query：none including empty ?。

**成功：** 200；HTML Orbit experimental。

**I/O / 执行：** Read embedded static bytes only; page load itself creates no light Job; subsequent explicit user actions call documented APIs.

**票据：** none

**错误分组/代码：** global, invalid_request, 404 not_found, 500 internal_error。

**成功头：** Content-Security-Policy: default-src none; script/style/connect self; base/frame/form none；Referrer-Policy: no-referrer。

来源：[internal/httpapi/light.go](../internal/httpapi/light.go)；测试：[light_test.go](../internal/httpapi/light_test.go)。

### GET /demo/orbit.js

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** none。Query：none including empty ?。

**成功：** 200；JS。

**I/O / 执行：** Read embedded static bytes only; page load itself creates no light Job; subsequent explicit user actions call documented APIs.

**票据：** none

**错误分组/代码：** global, invalid_request, 404 not_found, 500 internal_error。

**成功头：** Content-Security-Policy: default-src none; script/style/connect self; base/frame/form none；Referrer-Policy: no-referrer。

来源：[internal/httpapi/light.go](../internal/httpapi/light.go)；测试：[light_test.go](../internal/httpapi/light_test.go)。

### GET /demo/orbit.css

**版本：** 日常1d89与开发f8bb267均已实现。

**输入：** none。Query：none including empty ?。

**成功：** 200；CSS。

**I/O / 执行：** Read embedded static bytes only; page load itself creates no light Job; subsequent explicit user actions call documented APIs.

**票据：** none

**错误分组/代码：** global, invalid_request, 404 not_found, 500 internal_error。

**成功头：** Content-Security-Policy: default-src none; script/style/connect self; base/frame/form none；Referrer-Policy: no-referrer。

来源：[internal/httpapi/light.go](../internal/httpapi/light.go)；测试：[light_test.go](../internal/httpapi/light_test.go)。

### GET /demo/patterns.html

**版本：** 日常1d89无此路径；历史host56c/旧fe32507 bundle返回404；当前96a/f90扩展bundle已嵌入并返回200，已工作区安装96a；原1d89备份保留。

**输入：** 无body/query（包括空?），只GET。

**成功：** 完整扩展bundle实际嵌入后200静态资源；合法旧bundle没有可选文件时404标准Error。缺必需文件、profile/摘要/锁不符仍500。

**I/O / 执行：** 只读验证过的嵌入字节，不读取外部Web源码，不访问Store或设备，不创建Job；这不是新增硬件功能API。

**票据：** 无。

**成功头：** 原no-store/nosniff、CSP仅self的script/style/connect与no-referrer；Host/Origin保护不变，无CORS。

来源：[light.go](../internal/httpapi/light.go)、[embed.go](../internal/webui/embed.go)；测试：[light_test.go](../internal/httpapi/light_test.go)、[embed_test.go](../internal/webui/embed_test.go)。

### GET /demo/patterns.js

**版本：** 日常1d89无此路径；历史host56c/旧fe32507 bundle返回404；当前96a/f90扩展bundle已嵌入并返回200，已工作区安装96a；原1d89备份保留。

**输入：** 无body/query（包括空?），只GET。

**成功：** 完整扩展bundle实际嵌入后200静态资源；合法旧bundle没有可选文件时404标准Error。缺必需文件、profile/摘要/锁不符仍500。

**I/O / 执行：** 只读验证过的嵌入字节，不读取外部Web源码，不访问Store或设备，不创建Job；这不是新增硬件功能API。

**票据：** 无。

**成功头：** 原no-store/nosniff、CSP仅self的script/style/connect与no-referrer；Host/Origin保护不变，无CORS。

来源：[light.go](../internal/httpapi/light.go)、[embed.go](../internal/webui/embed.go)；测试：[light_test.go](../internal/httpapi/light_test.go)、[embed_test.go](../internal/webui/embed_test.go)。

### GET /demo/patterns.css

**版本：** 日常1d89无此路径；历史host56c/旧fe32507 bundle返回404；当前96a/f90扩展bundle已嵌入并返回200，已工作区安装96a；原1d89备份保留。

**输入：** 无body/query（包括空?），只GET。

**成功：** 完整扩展bundle实际嵌入后200静态资源；合法旧bundle没有可选文件时404标准Error。缺必需文件、profile/摘要/锁不符仍500。

**I/O / 执行：** 只读验证过的嵌入字节，不读取外部Web源码，不访问Store或设备，不创建Job；这不是新增硬件功能API。

**票据：** 无。

**成功头：** 原no-store/nosniff、CSP仅self的script/style/connect与no-referrer；Host/Origin保护不变，无CORS。

来源：[light.go](../internal/httpapi/light.go)、[embed.go](../internal/webui/embed.go)；测试：[light_test.go](../internal/httpapi/light_test.go)、[embed_test.go](../internal/webui/embed_test.go)。

## 开发版 Pattern v2 的额外限制

相对日常1d89，P6 f8bb267与Web host56c4aef组合源码新增12个规范method/path：P3的5个作者/存储操作与P6的4个固定引用预览/执行/查停操作；另有3个条件Web资源。总库存44个规范操作（含3个条件Web资源）；OpenAPI 为避免静态路径掩盖普通ID GET另列4个别名，共48个operation/43个path。这不是45套独立能力。

server 默认独立懒选 v2 库，Go handler 仍可不配库。纯作者preview零Store；引用preview需要Resolve来源；submit只在新key探库；query/cancel零Store/config。默认路径、flag/env、创建边界见第12节。P4/P5/P6及客户端已整合96a；历史原片和当前部署分别判断；CLI4e、作者eee和MCP16来源见第13节，当前96a安装状态只按第1节，不把原片软件测试冒作新实机。

保存重新构造来源候选并核对expected_revision，之后一次create-only Import；不匹配409且Store零调用。unknown完整合法记录可读/导出但不能新导入或执行；库含unknown使写操作read_only，已知单条仍可验证执行。publication uncertain只读核对原ID/完整记录，不自动重发。新submit失败不得替换旧活动任务，已接受原回执独立于后续库状态。

依据：[作者与存储README](../internal/httpapi/README.patterns-v2.zh-CN.md)、[固定引用执行README](../internal/httpapi/README.pattern-execution-v2.zh-CN.md)、[server接线](../cmd/server.go)、[准入核心](../internal/control/README.pattern-admission.zh-CN.md)、[来源resolver](../internal/patternresolver/README.zh-CN.md)。

## 错误外壳与分组

错误外壳统一为 `{"error":{"code":"...","message":"..."}}`；message可随操作系统变化，不当成固定机器字段。

### global

| HTTP | code / 语义 |
| --- | --- |
| 403 | local_only: loopback Host and same-origin Origin required |
| 405 | method_not_allowed + Allow header |
| 404 | not_found: unknown route/resource |

### json_1m

| HTTP | code / 语义 |
| --- | --- |
| 415 | json_required |
| 413 | body_too_large (1MiB), Connection close |
| 400 | invalid_json: strict object/unknown/duplicate/null/tail/type errors |

### json_64k

| HTTP | code / 语义 |
| --- | --- |
| 415 | json_required |
| 413 | body_too_large (65536B), Connection close |
| 400 | invalid_json: strict envelope; invalid_light_action from authoritative action parser |

### service

| HTTP | code / 语义 |
| --- | --- |
| 400 | invalid_request |
| 404 | not_found or request_not_found |
| 409 | idempotency_conflict / daemon_instance_changed / queue_full / busy |
| 410 | request_expired |
| 429 | idempotency_capacity |
| 503 | unavailable (offline/closed) |
| 500 | internal_error |
| 501 | idempotency_unsupported or frame_unsupported at optional service boundary |

### library

| HTTP | code / 语义 |
| --- | --- |
| 400 | invalid_pattern_request |
| 404 | pattern_not_found |
| 409 | pattern_library_limit / pattern_revision_conflict / pattern_conflict / pattern_library_busy |
| 501 | pattern_library_unsupported |
| 503 | pattern_library_unavailable / pattern_commit_uncertain |

### library_v2

| HTTP | code / 语义 |
| --- | --- |
| 400 | invalid_pattern_request |
| 409 | pattern_revision_conflict / pattern_library_read_only |
| 501 | pattern_source_unsupported or unconfigured pattern_library_unsupported |
| 503 | pattern_library_format_mismatch / pattern_commit_uncertain; uncertain response has no partial result/Location |

### pattern_execution_v2

| HTTP | code / 语义 |
| --- | --- |
| 400 | invalid_pattern_request；核心缓存能力 ErrInvalid 仍是 invalid_request |
| 404 | pattern_not_found / request_not_found / not_found |
| 409 | 原票据/实例/policy冲突、队列忙、pattern_revision_conflict / pattern_library_busy |
| 410 / 429 | request_expired / idempotency_capacity |
| 413 / 415 | body_too_large（4096B，关闭连接）/ json_required |
| 500 | pattern_integrity_error / internal_error；来源或冻结内容损坏不能包装成输入400 |
| 501 | pattern_execution_unsupported / pattern_library_unsupported / pattern_source_unsupported |
| 503 | unavailable / pattern_library_unavailable / pattern_library_format_mismatch；保留原票据，不等于未接受 |

## 兼容与证据限制

- All routes share loopback Host/same-origin Origin, no-store,nosniff. No authenticated public/cloud API or CORS relaxation.
- Strict JSON decoders reject unknown/duplicate/null/trailing fields; newer Action/Frame/Pattern codec additionally preserves nested numeric/Unicode grammar. Do not generalize every newest codec guarantee to all legacy routes.
- New Light/Frame/Color/Pattern routes reject query including empty ? where code checks ForceQuery; several old root/job routes do not uniformly reject query/body. Prefer documented canonical paths; this inventory does not claim missing guards are implemented.
- GET state is cached and may be stale while frame runs; latest sampled_at is evidence. Desired target, successful Job, readback and optical capture are different facts.
- OpenAPI覆盖本册44个规范操作（3个静态资源有bundle条件）与4个GET别名；JSONSchema不能证明原始词法、来源SHA或实际设备效果。

本文档校验依据固定源码、隔离软件测试、实际纯编译/Recorder样例与结构schema校验，校验过程未操作daemon、USB或摄像头；引用的96a实机与推广记录由独立窗口完成，第1节分别列明。完整结构化库存见 [low-http-inventory.json](api-http-inventory.json)，固定请求例子见 [low-http-examples.json](api-low-examples.json)。本稿不替代完整 SDK 签名；legacy CLI/SDK 可继续参考 [设备backend说明](../internal/devicebackend/README.md) 与 [SDK设备命令](https://github.com/b1ug/blink1-go/blob/b3f2586cc73073a7a0432889ea14a18c13247f46/device_cmd.go)。


## 15. 设计评估与下一步

已有基础是合理的：High level保留意图，Low level保留精确控制；颜色只有一个规范核心；预览、执行与保存分开；多入口沿同一worker，版本化来源与原回执可追踪。

需要继续完善的地方也很明确：

1. P4来源身份准入、P5验证resolver、P6 HTTP已完成；MCP16、CLI七命令与高级图案书Web已整合96a，三入口已有本机MK2有限验证；继续CLI list/import/export、USB重试诊断、参数与平台/代际验证。保存成功、已接受任务和实体效果仍分别判断。
2. 保持艺术动作可区分：blink是亮灭，breathe是对称起伏，pulse是attack/decay/rest，alternate是换位，rainbow是色相循环，orbit是两点相位/明暗流动。
3. 评审高级入口与低层入口的策略差异、pulse名义周期多出的30ms、hold的最后输出语义。当前文档如实写差异，未来只能通过明确新版本调整。
4. 新命名、组合/序列、用户palette、自动恢复和同步属于后续能力，不能让AI猜一个看似自然的词就执行。
5. 同步人类手册、OpenAPI、实际MCP schema、llms.txt与Skill；这些指导文件不增加权限，也不替代实际能力发现。

Web/HID直连、Homebrew、Codex hook、Starlark和正式网站按规划继续推进。本手册的可调用API不包含尚未实现的云桥、任意脚本执行或硬件Flash管理接口。

## 16. 机器可读资料、验证与版本

- [OpenAPI完整快照](openapi.json)：HTTP路径、请求和响应schema；条件关系、SHA/词法等须同时遵守说明，不能只靠JSON Schema判断。
- [实际MCP工具schema](mcp-tools.schema.json)：开发16工具的输入/输出与注释，另保留历史1d89的12工具快照，下载旧名不变。
- [HTTP操作目录](api-http-inventory.json)：逐操作的版本、状态、输入输出和源码依据。
- [JSON示例](examples/api/)：明确区分作者文件、低层Program、Frame和颜色输入。
- [Agent指南](agent-guide.zh-CN.md)及[项目Skill](../skills/blink1-control/SKILL.md)：使用入口；旧章节的历史状态以本手册和实际部署清单为准。

手册中的示例先做纯编译/预览和schema核对，不为编写文档重复操作硬件。日常执行验证继续绑定原始源码、设备和参数，不因本文更新自动升级为全平台支持声明。
