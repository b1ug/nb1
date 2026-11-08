# blink(1) 本地常驻控制：CLI 与 API 使用指南

本版把设备所有权与执行逻辑集中在同一个本地执行器：daemon 常驻持有选定设备，HTTP、CLI 和独立 MCP 适配器提交程序；每台设备由一个 worker 串行操作。`control --mode direct` 在 CLI 进程内运行同一个执行器。MCP 接入参见[独立适配器说明](../adapters/mcp/README.md)，其协议软件与实物验证状态分别记录。

这是工作区的本地候选版本，相关新代码尚未发布；既有 PR 保留待审状态，没有自动合并或修改远程 PR。本机已经在 macOS 27.2 arm64、blink(1) mk2、固件204上验证直控、HTTP设色、设备占用、取消、退出和读回。mk3没有真机验证；Windows/Linux 的 stub 编译结果不代表原生 USB I/O 通过。此命令不会安装系统服务，也不写设备 Flash 或模式内容。

## 1. 构建与启动

以下在 `nb1/` 仓库目录执行。现有工作区的根 `go.work` 会选择本地 SDK 与 GID 修复版：

```sh
go build -o ../bin/nb1-runtime .
../bin/nb1-runtime control --mode direct devices
../bin/nb1-runtime server --devices 20005601 --listen 127.0.0.1:8931
```

`direct devices` 只做新鲜枚举的元数据发现；不等同于型号已受支持。`server` 明确指定一个或多个真实序列号，例如 `--devices 20005601,20005602`，也可以沿用全局 `--device 20005601`。它不会默认打开第一个 HID。

启动后打印一行 JSON，其中 `url` 是实际监听地址。此时设备可能仍为 `connecting`，不表示已经在线。`--listen 127.0.0.1:0` 可选择空闲端口；`--port 8932` 保留为兼容用法，不能与显式 `--listen` 同时使用。默认地址是 `127.0.0.1:8931`，`--base` 非空路径尚不支持。

daemon 会先绑定端口，再尝试打开硬件；启动和只读监控不会重置现有灯光。正常控制任务开始前才停止旧的自主播放/tickle。设备连接失败时保持服务运行并重试，状态中保留错误。

另开终端使用下列命令；POSIX shell 示例可直接用于当前 Mac。Windows 需本机原生工具链与 USB 验证，命令行 HTTP 示例应使用 `curl.exe`。

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

直控 `set/run` 等待程序完成后才返回，成功的 `hold` 结果在释放句柄后保留。直控 `state` 是只读监控，不冻结、不熄灯，也不停止原有自主模式。直控中途被取消或超时时，活动程序仍按熄灯清理；不是“退出CLI就放任程序继续”。

daemon正在占用时，新旧CLI的合作式直控都会报设备占用。默认daemon模式连接失败不会静默回退到USB；需要直控时必须显式选择 `--mode direct`。跨命令的任务查询与取消使用daemon模式；单次直控退出后没有常驻任务存储。

## 4. 结构化程序

示例文件分为两类：`*.program.json` 是CLI读取的程序；`*.request.json` 是包含 `program/policy` 的HTTP请求。不要把HTTP信封直接交给 `control run`。

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
| `POST /v1/devices/{serial}/jobs` | 接受程序后返回202、job及Location |
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

普通提交体仍是 `{"program":{...},"policy":"replace"}`，`policy`省略或空串时为replace；无需先prepare。可选的请求二元组见下文。取消可不带请求体；如果携带，只接受 `application/json` 空对象。没有raw HID、固件升级或Flash写入入口。

状态中的 `desired` 是最后的逻辑目标，`observed` 是带时间戳的原始RGB采样。GET读取缓存，不额外并发访问USB；离线时旧采样仍可能保留。HTTP202、job终态、设备报告和相机观察是不同证据，不能互相替代。

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

`run` 接受相同两项flag，程序文件仍只包含Program。`prepare`只读取实例信息并在客户端生成16随机字节的nonce，不预留回执、不创建job、不操作USB。二元组必须同时提供非空值；`--mode direct`遇到prepare、request或任意回执flag会在设备锁/USB之前拒绝，包括显式空flag。没有自动重试、自动prepare或USB回退。

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

## 8. 所有权与服务退出

daemon、新直控和现有CLI使用同一用户、同一目录的操作系统文件锁。默认目录来自 `os.UserConfigDir()/nb1/device-locks`；可用 `NB1_LOCK_DIR` 或新命令的 `--lock-dir` 显式指定。所有合作进程必须一致。Unix目录须私有，锁文件会保留在磁盘；不要删除锁文件来“解锁”，真正的锁由打开的文件描述符持有，进程结束时释放。

这个机制不等于跨用户或跨服务账号的全局仲裁；不同用户的默认目录不同。官方工具等非合作程序不读取本项目锁，仍受各系统原生权限与独占规则影响。避免并行运行多个控制器。

按Ctrl-C或发送SIGTERM停止daemon，会取消任务、停止设备自主模式、熄灯、关闭句柄并释放锁。正常程序的 `hold` 不覆盖daemon退出的熄灯策略。常驻服务启动时保留当前输出；这里只在用户控制任务或明确退出策略中改变灯光。

HTTP仅绑定loopback，没有用户认证或远程访问模式；浏览器跨源请求会被拒绝，本地规划站也不会因此成为硬件控制台。MCP及后续消息队列、云桥作为薄入口，继续提交给同一个本地执行器，不另开USB控制循环。

## 9. 验证范围与下一步

当前本机验收包含精确原始RGB、直控保留颜色、只读查询、daemon启动保留输出、新旧直控占用保护、HTTP设色、非法请求无硬件改动、程序取消、正常退出熄灯与模式前后一致。相机用于定性观察，未作光学校准。

仍需继续验证真实拔插、非响应设备的原生超时、Windows/Linux原生USB、mk3与多设备实物场景。内核、HTTP和CLI的模拟/单元测试，不能替代这些真机结论。后续系统服务、MCP与消息输入的交付状态分别记录，不以这里的接口说明冒充已安装或已完成。
