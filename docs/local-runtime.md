# blink(1) 本地常驻控制：CLI 与 API 使用指南

设备发现范围：普通 `list` 使用 SDK 的 blink(1) 限定入口；只有显式 `list --all` 才请求全量 HID 元数据。两条路径都保留按序列号稳定排序；`--firmware` 才额外打开 blink(1) 查询固件，查询前仍检查设备所有权。daemon 与直控发现已复用同一 SDK 列表入口。

原生枚举何时生效取决于整合后的 SDK/GID 版本。本改动没有更新依赖 pin，需要与 SDK 的 `FindDevices(27b8,01ed)` 调用及 GID 原生精确过滤一起整合。此处只做 fake/构建验证，不证明输入监控授权弹窗已经消失，也不把枚举范围当成后台未就绪的唯一原因。

本版把设备所有权与执行逻辑集中在同一个本地执行器：daemon 常驻持有选定设备，HTTP、CLI 和独立 MCP 适配器提交程序；每台设备由一个 worker 串行操作。`control --mode direct` 在 CLI 进程内运行同一个执行器。MCP 接入参见[独立适配器说明](../adapters/mcp/README.md)，其协议软件与实物验证状态分别记录。权限与接口范围见[最小权限与控制边界](security-boundaries.zh-CN.md)。

这是工作区的本地候选版本，相关新代码尚未发布；既有 PR 保留待审状态，没有自动合并或修改远程 PR。本机已经在 macOS 27.2 arm64、blink(1) mk2、固件204上验证直控、HTTP设色、设备占用、取消、退出和读回。mk3没有真机验证；Windows/Linux 的 stub 编译结果不代表原生 USB I/O 通过。普通server/control命令不会安装系统服务，也不写设备 Flash 或模式内容；macOS/Linux候选用户服务管理须显式调用 `service install/start/stop/uninstall`；macOS当前机临时生命周期已验收并恢复原daemon，Linux仍待真实用户manager与原生USB验收。

## 1. 构建与启动

以下在 `nb1/` 仓库目录执行。本候选的 `go.mod` 已固定 SDK `v0.0.2-0.20261108210614-0336a1a2aa92` 与 GID `v0.0.3-0.20261108194932-7d9978debbdf`，单独拉取仓库也会使用这些修复。它们是已推送提交对应的开发伪版本，没有创建正式发布。开发工作区仍可覆盖依赖；要验证没有本地覆盖，显式关闭工作区：

```sh
GOWORK=off go list -m github.com/b1ug/blink1-go github.com/b1ug/gid
GOWORK=off go build -mod=readonly -o ../bin/nb1-runtime .
../bin/nb1-runtime control --mode direct devices
../bin/nb1-runtime server --devices 20005601 --listen 127.0.0.1:8931
```

`direct devices` 只做新鲜枚举的元数据发现；不等同于型号已受支持。`server` 明确指定一个或多个真实序列号，例如 `--devices 20005601,20005602`，也可以沿用全局 `--device 20005601`。它不会默认打开第一个 HID。

启动后打印一行 JSON，其中 `url` 是实际监听地址。此时设备可能仍为 `connecting`，不表示已经在线。`--listen 127.0.0.1:0` 可选择空闲端口；`--port 8932` 保留为兼容用法，不能与显式 `--listen` 同时使用。默认地址是 `127.0.0.1:8931`，`--base` 非空路径尚不支持。

daemon 会先绑定端口，再尝试打开硬件；启动和只读监控不会重置现有灯光。正常控制任务开始前才停止旧的自主播放/tickle。设备连接失败时保持服务运行并重试，状态中保留错误。

另开终端使用下列命令；POSIX shell 示例可直接用于当前 Mac。Windows 需本机原生工具链与 USB 验证，命令行 HTTP 示例应使用 `curl.exe`。

### 只读诊断

```sh
../bin/nb1-runtime doctor
../bin/nb1-runtime doctor --serial 20005601 --json
../bin/nb1-runtime doctor --endpoint http://127.0.0.1:8932
```

`doctor` 在3秒总预算内读取当前可执行文件的Go、OS、架构、CGO、原生HID后端与SDK/GID构建版本，再依次GET `/healthz` 和 `/v1/devices`。它不扫描或打开USB、不提交控制任务、不安装服务；daemon不可达时也不回退直控。省略 `--serial` 时显示daemon配置的全部目标；诊断目标不从旧的设备配置中自动选择。

HTTP响应、`api_version: v1`、设备 `online` 和 `observed` 新鲜度分别报告。`observed` 是daemon已有的带时间戳快照，doctor不会触发新采样；超过5秒标为 `stale`，缺失和明显未来时间另行提示。5秒是本命令针对默认约1秒采样周期的诊断阈值，不是实时保证。已有活动任务与排队深度会显示，正常工作中的任务不会被视为故障。`busy` 提示通过现有持有者控制或正常停止占用进程，不能通过删除锁文件抢占。

构建版本取自本机CLI的 `debug.ReadBuildInfo`，不读取当前目录的 `go.mod`，也不代表另一个daemon进程的二进制版本；此命令检查API版本，不核对daemon实例身份。若工作区或 `replace` 使用未编号的本地源码，会显示 `local_unversioned`，不会把请求的模块版本冒充实际构建版本；不输出本地替换路径、任意构建参数或环境变量。`native_hid_supported` 只表示二进制包含原生后端，不能替代当前系统版本和具体硬件的实物验收。CGO=0的CLI仍可通过HTTP诊断独立运行的daemon。

`--json` 的结构版本是 `schema_version: 1`。所有检查通过时 `status: ok`、退出码0；服务不可达、API不匹配、设备离线或快照异常时，先在stdout输出完整 `status: attention` 报告，再以退出码1返回，错误摘要在stderr。脚本应分别处理stdout与stderr，并检查 `issues[].code` 与建议；参数错误或写输出失败也会返回非零。doctor自身的构建与HTTP模拟测试不能替代USB实物验收，也不改变已有硬件兼容性结论。

### macOS用户级持久启动候选

`service render` 继续只生成配置。新增的 `service install/start/stop/status/uninstall` 只针对当前普通用户的固定LaunchAgent：install先只读核对launchd归属，再写托管plist；**即使不加 `--start`，下次图形登录也可能自动加载**。安装加 `--start` 或对已安装配置执行 `service start` 才会立即bootstrap；相同配置已注册时不重启。`service stop` 只注销已核对归属的注册项并保留plist，**不等于禁用下次图形登录加载**；注册消失不表示进程退出或USB清理已完成。卸载已注册项须显式 `--stop`。`service status --config-only` 完全不调用launchctl；普通status区分磁盘安装与launchd注册，不把注册当作进程、HTTP或设备就绪。

macOS部分除fake launchctl回归外，已在当前Mac/选定mk2完成临时安装、独立start/stop、保留配置、幂等不重启、真实退出后的direct读黑及恢复原daemon的19项检查，临时配置已清理；没有验证注销/登录/重启生命周期。未知plist、归属不明、查询失败、停止不确定或配置被修改时均保留文件；原地升级替换尚未实现。完整用法和边界见[用户级启动配置与管理](../internal/service/README.zh-CN.md)。Linux候选现已接入相同CLI，使用独立systemd用户管理器；仅有模拟与构建证据，真实Linux user-manager CI与原生USB待验收。Windows候选另有仅查询固定当前用户任务的只读service status，自动管理未实现；其他平台仍仅render。

### Linux用户级持久启动候选

Linux上的 `service install/start/stop/status/uninstall` 仅管理当前普通用户的固定 `nb1.service`，不接收任意UID或跨平台管理目标、不自动提权。install默认创建受控unit和 `default.target.wants` 持久启用链接，并刷新用户manager；不加 `--start` 就不立即启动，但下次登录或用户manager启动可能加载。start只立即启动，stop保留unit和登录启用；active unit卸载须加 `--stop`，loaded但inactive可直接卸载。

`status --config-only` 不调用systemctl。Linux JSON原样报告独立状态结构，分别列出磁盘配置、启用链接、loaded、active、manager看到的unit_file_state及归属；这些都不证明HTTP可用或USB在线。未知来源、drop-in/覆盖、配置变化、查询或控制结果不明时保守拒绝，不改用sudo或系统服务。实现和CLI只完成模拟/构建验证，真实Linux用户manager CI及Linux原生USB仍需分别验收。用法与人工部署替代方式见[用户级服务说明](../internal/service/README.zh-CN.md)。

### Windows候选：只读任务状态，不管理任务

Windows目前保留跨平台 `service render`，新增候选 `service status` / `service status --json` 仅查询经过token核验的当前普通用户固定任务 `\B1UG-nb1-<SID>`，不接收其他SID、任务名或远程机器。查询器使用系统API定位的受保护PowerShell，固定代码与stdin JSON分开；子进程读取SID也只申请Token Query权限。普通用户原生查询和缺失HRESULT合同尚待独立验收，软件或跨编译结果不代替Windows支持。

`presence=present`只是同名注册存在，`ownership=not_checked`不证明托管归属。有限字段只有启用状态、Scheduler数值/名称和上次任务结果；Scheduler ready/running不等于daemon PID、HTTP或USB就绪。查询失败先输出原Windows状态再返回非零；当前GetTask的0x80070002也保留unknown/not_found_unverified，不能当作任务不存在。身份/PowerShell信任构造拒绝时不编造状态、提权或换身份。

Windows的 `install/start/stop/uninstall` 与 `status --config-only` 都在构造查询器前明确unsupported；没有写入manager、注册/运行/停止任务或可靠退出协议。Mac/Linux管理行为保持原合同。详见[用户级服务说明](../internal/service/README.zh-CN.md)与[查询层边界](../internal/service/taskquery/README.zh-CN.md)。

## 2. 查看设备与设色

```sh
../bin/nb1-runtime control devices
../bin/nb1-runtime control state 20005601
../bin/nb1-runtime control set 20005601 '#800000' --led 1 --fade-ms 100 --wait
```

`control` 默认使用 daemon。端口不同可加 `--endpoint http://127.0.0.1:8932`。这里的 `devices` 列出 daemon 配置的设备，包含离线目标；它不是所有 USB 的即时扫描。

`set` 等价于一个步骤、一次执行、`on_finish: hold` 的程序。`--led 0` 控制两颗灯，1/2控制单颗；颜色必须是 `#RRGGBB`，淡入时间必须为10毫秒的整数倍。

| 入口 | 输入 `#800000` 的含义 |
| --- | --- |
| 新 `control set` / v1 API | 原始RGB，预期设备报告为 `#800000`；不隐式做Gamma |
| 旧 `fade` / Controller 默认模式 | 原有Gamma行为保留；当前mk2曾读回 `#370000` |

不要在新入口再次套用旧入口的Gamma补偿。设备报告RGB也不是经过光学校准的实际亮度。

不加 `--wait` 时会输出已接受的job及其ID；加上后轮询到终态。客户端等待超时不会取消已经由daemon接受的job。

## 3. 直控模式

先停止占用同一设备的daemon，再执行：

```sh
../bin/nb1-runtime control --mode direct set 20005601 '#800000' --led 1 --fade-ms 100
../bin/nb1-runtime control --mode direct state 20005601
```

直控 `set/run/effect` 等待程序完成后才返回，成功的 `hold` 结果在释放句柄后保留。直控 `state` 是只读监控，不冻结、不熄灯，也不停止原有自主模式。直控中途被取消或超时时，活动程序仍按熄灯清理；不是“退出CLI就放任程序继续”。

daemon正在占用时，新旧CLI的合作式直控都会报设备占用。默认daemon模式连接失败不会静默回退到USB；需要直控时必须显式选择 `--mode direct`。跨命令的任务查询与取消使用daemon模式；单次直控退出后没有常驻任务存储。

## 4. 结构化程序

示例文件分为三类：`*.program.json` 是 `control run` 的程序；`*.effect.json` 是 `control effect` 的高层意图；`*.request.json` 是HTTP信封，必须且仅有 `program/effect` 之一，可带policy与回执二元组。不要把HTTP信封直接交给CLI的run或effect。

```sh
../bin/nb1-runtime control run 20005601 examples/runtime/sequence.program.json --wait
../bin/nb1-runtime control run 20005601 examples/runtime/long-running.program.json
```

程序示例：

```json
{
  "steps": [
    {"led": 1, "color": "#ff0000", "fade_ms": 100, "hold_ms": 500},
    {"led": 2, "color": "#0000ff", "fade_ms": 100, "hold_ms": 500}
  ],
  "repeat": 2,
  "on_finish": "off"
}
```

| 字段 | 当前契约 |
| --- | --- |
| `steps` | 必须有1–1024项；每项必须有合法颜色 |
| `led` | 0/1/2，省略为0 |
| `color` | `#RRGGBB`，接受大小写，返回时转小写；不接受自然语言颜色 |
| `fade_ms` | 0–600000，10的整数倍；省略为0 |
| `hold_ms` | 0–3600000；省略为0 |
| `repeat` | 省略或0规范为1；范围1–10000；0不表示无限循环 |
| `on_finish` | `off`或`hold`；省略或空串为`off` |
| 总时间 | `repeat × Σ(fade_ms + hold_ms + 10)` 不超过24小时；这是名义预算，原生I/O与调度还会增加实际耗时 |

JSON必须是一个对象，最多1MiB；未知字段、非规范字段大小写、显式 `null`、重复对象键、尾随JSON与超过64层的嵌套均拒绝。整数字段使用普通十进制JSON整数，不发送 `1.0` 或 `1e0`。CLI程序与HTTP请求使用相同的严格输入入口。

`on_finish: hold` 表示在正常结束时停止自主活动并冻结当前设备读回颜色；它不改变活动任务被取消时的熄灯策略。`sequence.program.json` 默认结束熄灯；`long-running.program.json` 适合观察和取消，若在直控中完整等待，请将 `--timeout` 提高到例如45秒。

### 高层效果：steady 与 pulse

当前候选新增两种有限效果，统一调用 `control.CompileEffect` 编译为普通Program；不另建定时器、设备所有者或持久播放模式。新二进制与真机验收状态单独记录，旧主路径通过不意味着这些新增效果已经上机验证。

```sh
../bin/nb1-runtime control effect 20005601 examples/runtime/steady.effect.json --wait
../bin/nb1-runtime control effect 20005601 examples/runtime/pulse.effect.json --policy queue --wait
```

daemon模式发送原effect，由服务端权威编译，再走同一Submit/回执路径；返回job中的program是编译后的实际程序。direct模式先严格解码文件并调用同一编译器，所有字段与编译错误发生在设备锁/USB之前。两种模式继续使用原policy、等待、取消和退出清理；direct不接受回执扩展。

```json
{"kind":"steady","color":"#102030","led":0,"fade_ms":100}
```

steady只允许kind、color、led、fade_ms，编译为一个步骤、repeat=1、on_finish=hold。省略fade_ms为0，范围和10ms单位与Program相同。不能提供cycles、period_ms或on_finish，即使给0或hold也拒绝。

```json
{"kind":"pulse","color":"#102030","led":0,"cycles":2,"period_ms":800,"on_finish":"off"}
```

| pulse字段 | 契约 |
| --- | --- |
| color / led | 同Program的原始RGB和0/1/2选择 |
| cycles | 省略为1，范围1–10000；显式0不表示无限 |
| period_ms | 省略为1000，范围60–1200000，10ms整数倍 |
| on_finish | 省略off；显式仅off或hold，空串拒绝 |
| fade_ms | pulse不接受，即使为0也拒绝 |

pulse每周期执行同一LED的黑→目标色→黑三个步骤。令 `U=floor(period_ms/20)*10`、`D=period_ms-U`，三个fade_ms分别为0、U−20、D−10，hold_ms均0；cycles保持为Program.repeat，不展开步骤。800ms得到0/380/390，再计三个10ms更新等待正好是800ms名义周期；810ms得到0/380/400。原生I/O、调度与前后清理还会增加墙钟耗时，这不是硬实时保证。`cycles × period_ms`仍受24小时总预算校验。

pulse的hold保留的是结束时读回：选中的LED此时已为黑色，不是目标色。off、活动取消和daemon退出沿用整设备熄灯策略；未选中LED不等于获得了独立任务所有权。所有颜色仍是原始RGB，不自动Gamma，也不上传设备Pattern或Flash。

HTTP可使用 `{"effect":{...},"policy":"replace"}`，参见 [pulse-effect.request.json](../examples/runtime/pulse-effect.request.json)。program与effect缺失或同时出现都拒绝；内部null、未知字段、重复键、非规范大小写等严格规则不变。

效果可沿用prepare产生的回执二元组。去重比较编译后规范化的Program与policy，而非原始effect JSON，所以同一实例/设备/键下，effect与完全等价的raw program返回同一job；编译后不同的有效程序返回冲突，非法shape或编译失败直接拒绝且不触发replace。

## 5. 队列、替换与取消

```sh
../bin/nb1-runtime control run 20005601 examples/runtime/sequence.program.json --policy queue
../bin/nb1-runtime control run 20005601 examples/runtime/sequence.program.json --policy reject
../bin/nb1-runtime control set 20005601 '#00ff00' --policy replace
../bin/nb1-runtime control job JOB_ID
../bin/nb1-runtime control cancel JOB_ID --wait
```

把 `JOB_ID` 替换为提交响应中的完整ID，包括 `job_` 前缀。

| 策略 | 行为 |
| --- | --- |
| `replace` | 默认值。取消旧活动job与其队列；活动job完成停止/熄灯清理后，新job才开始 |
| `queue` | 追加到设备有界队列，默认最多8个待执行job，不包含当前活动job |
| `reject` | 设备已有活动或排队工作时返回占用错误 |

活动job的状态通常是 `queued → running → succeeded`；取消先成为 `cancelling`，停止软件步骤、设备自主模式、熄灯并检查读回后才成为 `cancelled`。停止或读回失败时是 `failed`，不会伪报已取消。

取消尚未运行的排队job会直接移除，不干扰另一个活动job，因此该设备可能继续亮灯。对已终结job重复取消，返回已有终态。

当前默认在内存中保留全局最近128条终态job，活动和排队记录另行保留。普通历史被淘汰后job查询可能404；显式启用的有效回执独立保留，仍能返回原job。daemon重启会丢失两者，没有磁盘恢复或跨重启任务续跑。

## 6. HTTP API

机器可读契约为 [openapi.json](openapi.json)，使用已核对的 [OpenAPI 3.2.1](https://spec.openapis.org/oas/v3.2.1.html) 和JSON Schema 2020-12。当前作为本地文件交付，没有宣称存在 `/openapi.json` 网络路由。客户端不主动重试提交；Go HTTP传输层可能安全重试只读GET，回执查询始终不创建任务。查询失败只能说明回执暂不可得，不能推断旧意图未执行。

| 方法与路径 | 返回 |
| --- | --- |
| `GET /healthz` | HTTP服务是否响应；不表示硬件在线 |
| `GET /v1/instance` | 可选请求回执能力、实例ID、准入前缀与限额；不提交任务 |
| `GET /v1/devices` | daemon配置设备的缓存状态数组 |
| `GET /v1/devices/{serial}` | 单设备状态；离线目标仍可返回200状态记录 |
| `GET /v1/transport-metrics` | SDK操作级 calls/attempts/retries/failures 累积快照 |
| `POST /v1/devices/{serial}/jobs` | 接受program或effect后返回202、job及Location |
| `GET /v1/devices/{serial}/requests/{request_id}?expected_daemon_instance_id=…` | 原实例下的请求回执与job最新状态 |
| `GET /v1/jobs/{id}` | job当前状态与步骤进度 |
| `POST /v1/jobs/{id}/cancel` | 返回202及取消请求处理后的真实job状态 |

```sh
curl -sS http://127.0.0.1:8931/healthz
curl -sS http://127.0.0.1:8931/v1/devices/20005601
curl -sS http://127.0.0.1:8931/v1/transport-metrics
curl -i -X POST http://127.0.0.1:8931/v1/devices/20005601/jobs \
  -H 'Content-Type: application/json' \
  --data-binary @examples/runtime/set-red.request.json
curl -sS -X POST http://127.0.0.1:8931/v1/jobs/JOB_ID/cancel
```

普通程序提交体仍是 `{"program":{...},"policy":"replace"}`；高层效果使用互斥的 `effect` 字段。`policy`省略或空串时为replace，无需先prepare。可选的请求二元组见下文。取消可不带请求体；如果携带，只接受 `application/json` 空对象。没有raw HID、固件升级或Flash写入入口。

状态中的 `desired` 是最后的逻辑目标，`observed` 是带时间戳的原始RGB采样。GET读取缓存，不额外并发访问USB；离线时旧采样仍可能保留。HTTP202、job终态、设备报告和相机观察是不同证据，不能互相替代。

活动程序跨步骤共享观测截止时间，短步骤不会重置采样周期；观测由同一个设备worker尽力串行执行，读取、重试或阻塞可能增加实际时长，名义周期并非硬实时保证。

`transport-metrics` 的 `calls` 是逻辑SDK操作数，`attempts` 包含初次尝试，`retries` 为额外尝试，`failures` 为最终失败。一次查询操作可能含命令写入和报告读回，因此这些数值不是raw USB transfer数量。`last_error` 保留历史错误，即使该操作已由重试恢复；进程重启会重新计数。

当前mk2兼容层仅对可证明幂等的颜色/停止命令与完整查询事务进行有限重试，每个操作最多3次尝试；不在SDK/GID下方全局重放，也不自动重试raw命令、模式或Flash写入。首次1000 job测试在326个成功后遇到原生general error；修复后的1000/1000通过样本仍发生4次局部重试，不能宣传成原生零错误。具体样本和范围见本地进展记录。

## 7. 错误、超时与重试

错误体固定为：

```json
{"error":{"code":"busy","message":"device busy"}}
```

| HTTP | 常见code与处理 |
| --- | --- |
| 400 | `invalid_json` / `invalid_request`，修正输入后再提交 |
| 403 | `local_only`，使用loopback Host；浏览器请求须同源 |
| 404 | `not_found`，目标未配置、job不存在或历史已淘汰 |
| 404 | 回执 `request_not_found` 只表示查询当时未找到；原POST可能仍在途中 |
| 409 | `busy` / `queue_full`；回执还区分 `idempotency_conflict` 与 `daemon_instance_changed` |
| 410 | `request_expired`，无可用回执且token过期；不能判断旧意图是否执行 |
| 429 | `idempotency_capacity`，回执数量/步数预算已满；不驱逐未过期记录 |
| 413 / 415 | 请求过大 / JSON Content-Type错误 |
| 503 | `unavailable`，设备离线、未准备好或执行器已关闭；查设备状态 |
| 500 | `internal_error`，检查具体错误和设备连接；不要假设原生操作已经停止 |
| 501 | `idempotency_unsupported`，该服务没有可选回执扩展；不能移除字段后自动降级提交 |

无键提交在取得明确的参数、设备或队列拒绝时，该次提交没有创建新job。如果连接在发送或读取响应时超时、重置，没有收到有效响应，结果可能未知：服务端可能已经接受。拿到job ID就查询；没拿到时，设备的 `active_job/observed` 不能可靠关联此前请求。不要对未知结果自动重发非幂等序列。

### 可选请求回执：先保存原二元组，再提交

该扩展用于应对提交响应丢失，不改变旧命令的默认行为。当前候选代码提供接口，运行中的旧二进制不一定包含它；以 `control prepare` 或 `/v1/instance` 的实际响应为准。普通设色不需要增加准备步骤。

```sh
../bin/nb1-runtime control prepare
# 将输出的两个完整字符串保存到变量；不要自己重算或修改实例。
REQUEST_ID='粘贴prepare输出的request_id'
DAEMON_INSTANCE_ID='粘贴prepare输出的expected_daemon_instance_id'
../bin/nb1-runtime control set 20005601 '#102030' \
  --request-id "$REQUEST_ID" --expected-daemon-instance-id "$DAEMON_INSTANCE_ID"
../bin/nb1-runtime control request 20005601 "$REQUEST_ID" \
  --expected-daemon-instance-id "$DAEMON_INSTANCE_ID"
```

`run`和`effect`接受相同两项flag，文件仍只包含各自的Program或Effect。`prepare`只读取实例信息并在客户端生成16随机字节的nonce，不预留回执、不创建job、不操作USB。二元组必须同时提供非空值；`--mode direct`遇到prepare、request或任意回执flag会在设备锁/USB之前拒绝，包括显式空flag。没有自动重试、自动prepare或USB回退。

同一实例、同一设备、同一request_id与规范化后相同的program/policy，只准入一个逻辑任务；重复提交仍返回HTTP202和原job的最新状态，即使已经failed/cancelled/succeeded，也不重新运行。不同payload返回409 `idempotency_conflict`。HTTP响应头 `X-NB1-Replayed` 与 `X-NB1-Daemon-Instance-ID` 标明结果；带键提交的Location指向回执查询。CLI输出仍是job，`--wait`在带键时查询回执，不依赖普通128条历史。

HTTP样例见 [with-receipt.request.json](../examples/runtime/with-receipt.request.json)：其中固定示例键和实例仅用于说明格式，真正提交前必须替换为本次prepare的二元组。两个字段不能单独提供，显式null、重复键及字段大小写歧义仍被拒绝。

| 情况 | 正确处理 |
| --- | --- |
| 提交响应丢失 | 保留原实例、键、设备和原payload；先查询，调用方可显式重发完全相同的绑定与payload |
| 查询404 `request_not_found` | 仅说明查询当时没有记录，原POST可能尚在途中；不能据此自动换键 |
| 410过期或409实例变化 | 旧执行结果可能未知；不自动绑定到新实例或获取新键补发 |
| 明确的新键容量/参数拒绝 | 此次未准入；处理原因，不能把它当作此前未知请求未执行的证明 |

请求ID前缀的截止点是daemon的单调运行毫秒，不是墙钟。新键准入窗口默认60秒；到期后未命中的旧键拒绝执行。终态回执至少保留完成后60秒且不早于token截止点，活动回执不因TTL被删，查询/重放不续期。当前默认4096条与65536个保留steps，不展开repeat；额度满时新键429，已有有效键仍可查/重放。

普通历史与回执均在内存中。重启产生新实例，断线重连本身不换实例。该机制保证一个键最多准入一个逻辑任务，不保证任务成功、USB只发送一次或跨重启exactly-once；runtime既有安全幂等操作的局部USB重试是另一层机制。回执扩展新增的软件测试不等于新增真机验收。

`--timeout` 默认30秒，是CLI请求/直控执行的等待预算；HTTP客户端单次传输另有10秒上限。等待daemon任务超时不取消任务。直控中断会请求取消自己的活动任务，并等待清理。

`--shutdown-timeout` 默认10秒，只限制等待清理的时间，不会凭空中止同步原生调用。内核不会为了快速返回而提前释放仍在I/O中的句柄及设备锁。若最终主进程退出，由操作系统回收资源；deadline错误本身不证明USB已停止。

读取或任务清理已确认故障时，执行器会先对外发布离线状态、终结排队任务并拒绝新的控制提交，再等待旧句柄关闭；关闭期间仍保留设备锁，不承诺底层 Close 在固定时间内返回。

### 命令完成后通知灯

[Python 标准库示例](../examples/command-notify/README.zh-CN.md)可在构建、测试或 Agent 命令结束后短时显示成功/失败颜色，使用同一有限 Program 结束熄灯。默认只预览；显式 `--execute` 并提供新报告路径才执行。示例包装 `control prepare` 与带实例回执的 `control run --wait`，不另建 HTTP 或 USB 路径；未知结果保留原绑定，不自动重提。子命令退出码与通知结果分别记录。

## 8. 所有权与服务退出

daemon、新直控和现有CLI使用同一用户、同一目录的操作系统文件锁。默认目录来自 `os.UserConfigDir()/nb1/device-locks`；可用 `NB1_LOCK_DIR` 或新命令的 `--lock-dir` 显式指定。所有合作进程必须一致。Unix目录须私有，锁文件会保留在磁盘；不要删除锁文件来“解锁”，真正的锁由打开的文件描述符持有，进程结束时释放。

这个机制不等于跨用户或跨服务账号的全局仲裁；不同用户的默认目录不同。官方工具等非合作程序不读取本项目锁，仍受各系统原生权限与独占规则影响。避免并行运行多个控制器。

按Ctrl-C或发送SIGTERM停止daemon，会取消任务、停止设备自主模式、熄灯、关闭句柄并释放锁。正常程序的 `hold` 不覆盖daemon退出的熄灯策略。常驻服务启动时保留当前输出；这里只在用户控制任务或明确退出策略中改变灯光。

HTTP仅绑定loopback，没有用户认证或远程访问模式；浏览器跨源请求会被拒绝，本地规划站也不会因此成为硬件控制台。MCP及后续消息队列、云桥作为薄入口，继续提交给同一个本地执行器，不另开USB控制循环。

## 9. 验证范围与下一步

当前本机验收包含精确原始RGB、直控保留颜色、只读查询、daemon启动保留输出、新旧直控占用保护、HTTP设色、非法请求无硬件改动、程序取消、正常退出熄灯与模式前后一致。相机用于定性观察，未作光学校准。

仍需继续验证真实拔插、非响应设备的原生超时、Windows/Linux原生USB、mk3与多设备实物场景。内核、HTTP和CLI的模拟/单元测试，不能替代这些真机结论。后续系统服务、MCP与消息输入的交付状态分别记录，不以这里的接口说明冒充已安装或已完成。

## 只读编译预览与能力目录

`GET /v1/capabilities` 返回软件编译契约 `nb1-program-effect/v1`、可用输入/效果、精确限制和颜色语义。它不扫描设备；每台设备实际启用的能力仍看已有设备缓存快照。本版命名 Pattern 库和硬件 Pattern 上传均为 `false`。

`POST /v1/programs/preview` 接受 `program` 或 `effect` 恰好一种，可选 `serial`。它调用实际 `CompileEffect`/`Normalize`，返回规范化 Program、总步骤、名义时长和所需能力，不创建 Job、不准备回执、不拿设备锁、不打开 USB。预览不接受执行策略、票据或尚未实现的 `pattern_ref`。

```json
{"effect":{"kind":"pulse","color":"#00a000","cycles":2,"period_ms":800}}
```

此例展开三步骤（淡入/淡出 380/390ms），重复两次，共六步骤，名义时长 1600ms，结束熄灯。名义时长包含每步骤 10ms 更新余量，不包括原生 I/O、调度和前后清理的额外耗时。

省略 `serial` 时只编译；提供时只读取一个已配置设备的缓存：在线且声明的所需能力齐全为 `supported`，已知缺少为 `unsupported`，不在配置/离线/能力信息不足为 `unknown`。这些结果不是执行、任务准入或硬实时保证；忙碌、只读运行模式和后续断开仍可能使提交失败。

CLI 默认通过 daemon：`nb1 control capabilities`、`nb1 control preview --effect effect.json` 或 `--program program.json`，可选 `--serial 20005601`。显式 `--mode direct` 的预览/目录只运行本机编译器，不打开设备；设备快照检查必须使用 daemon。旧 daemon 没有该能力时明确报不支持，不自动回退直控。

MCP 使用新的只读 `preview_program`，可选 `list_devices` 的 `include_capabilities:true` 读取目录。原六工具与原 `dry_run` 行为保留：Effect 的旧 dry_run 只校验结构，不访问 daemon，也不能证明完整编译成功。新预览成功是 HTTP200 和 `program_preview`，不是接受 Job；即使 HTTP 超时也没有任务被该接口提交，不自动重试。

这是命名 Pattern 持久化之前的共同编译基础。软件库、历史持久化、重启恢复与设备 RAM/Flash 各自独立，不因增加预览接口而开放。
