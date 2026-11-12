# 本地 Web 调试器与历史记录

当前候选的使用与嵌入边界见 [Web presentation](web-typography.md)。当前只接受 `static-editor/v1` 的完整闭合产物：26 个声明资源加 `manifest.json`，共 27 文件，含 JSON 编辑器脚本、样式与完整许可；总预算为 640 KiB。合同为 `nb1-api/v1`。来源提交、逐文件摘要、manifest 与 lock 仍需全部匹配；不是按历史 profile 自动降级。

当前导入仍使用 `tools/import-web-dist.py`，显式指定干净构建的完整 source commit 与 manifest SHA-256；`--check` 和 `--dry-run` 不改文件。导入后在当前模块运行 `GOWORK=off CGO_ENABLED=0 go test ./internal/webui ./internal/httpapi`。三个 HTML 响应会为编辑器动态样式注入每次新生成的 nonce；只有 meta 占位改变，嵌入源与 manifest 保持静态。随机源或占位校验失败时返回 JSON 500，不提供缺少保护的 HTML。

## 历史快照

以下内容记录所列历史构建的 API、profile、容量与安装证据。旧 profile、256 KiB 预算、Program 与票据流程不代表当前候选；重现旧提交需使用与其匹配的旧宿主和工具。历史指标原样保留，不作为当前使用说明。

此页面由 nb1 的 HTTP 服务直接提供：`/demo/`。HTML、CSS 和 JavaScript 嵌入二进制，所有 API 请求使用同一个 origin；不依赖 Node、CDN、外部脚本或云端存储。

Web 作者文件由独立的本地 `nb1-web` 仓库维护。nb1 只跟踪 `internal/webui/dist` 中的固定构建产物及其 `manifest.lock.json`，不在 Go 构建或启动时查找 Web 源码、运行 Node 或下载资源。原有六个 `/demo/` 资源地址与 HTTP 安全策略保持一致；扩展产物还可提供 `/demo/patterns.html`、`patterns.js`、`patterns.css`。首页地址是 `/demo/`，相对导航使用 `./`。

## 导入固定 Web 产物

先在 Web 仓库产生干净提交对应的 dist，保存其完整 source commit 与 `manifest.json` 的 SHA-256，再在 nb1 仓库运行：

```sh
python3 tools/import-web-dist.py /absolute/path/to/dist \
  --source-commit FULL_SOURCE_COMMIT --manifest-sha256 MANIFEST_SHA256 --check
python3 tools/import-web-dist.py /absolute/path/to/dist \
  --source-commit FULL_SOURCE_COMMIT --manifest-sha256 MANIFEST_SHA256 --dry-run
python3 tools/import-web-dist.py /absolute/path/to/dist \
  --source-commit FULL_SOURCE_COMMIT --manifest-sha256 MANIFEST_SHA256
```

前两条只读。导入接受三个完整、闭合的产物组合：基础组合为 `index.html`、`demo.css`、`demo.js`、`orbit.html`、`orbit.css`、`orbit.js`、`LICENSE.txt`；扩展组合在此基础上要求全部三个 `patterns.html/js/css`；双语扩展再同时要求`i18n.js`与`locales.js`。基础组合必须声明原来的两项 light 合同，扩展组合还必须声明 `nb1-pattern-library/experimental-v2` 与 `nb1-pattern-record/experimental-v2`。不接受只加入一两项新资源或合同，也不接受用已知新资源替换旧文件。

整个 dist 含 manifest 仍至多 256 KiB。导入检查来源、原 manifest 字节、逐文件大小和摘要，并按 ASCII 路径排序后将 `path + NUL + sha256 + LF` 拼接为 UTF-8，重算七项、十项或十二项资源的 `source.tree_sha256`。dirty 来源、未知文件、重复字段、null、符号链接、重解析点与硬链接均拒绝。独立 Web 仓库的源码提交和锁文件仍需由构建方审查；导入器验证的是明确指定的产物，不替代来源可信度判断。

可在明确重新指定旧来源及摘要后导入完整基础组合用于恢复。有效基础组合没有新页面时，其三个新资源返回404；必需资源缺失或摘要不符仍视为整个bundle校验失败，不以404掩盖。新增页面沿用同源、只允许GET、无query/body、no-store/nosniff、严格CSP与no-referrer。是否实际嵌入新页面，以本二进制的固定dist为准；单独升级host代码不构成新Web功能验收。

导入在临时目录复制校验后更换生成目录和锁；普通错误会恢复旧版。它不承诺两个路径在崩溃时原子切换，不支持与 Go 构建并发执行。中断残留或不一致的生成目录会拒绝继续；Go 在读取任何页面前还会校验完整的嵌入文件集与锁，失败时不提供页面。`cleanup_warnings` 非空表示新产物可能已安装，但残留需检查，不能只凭命令退出码推断旧产物仍在。

导入后运行 `go test ./internal/webui ./internal/httpapi`。浏览器验证与硬件验收分别记录；一次构建的来源由 Web commit、manifest SHA、nb1 commit 和最终二进制 SHA 共同描述。本地导入不上传、不创建 Release，也不修改系统服务。

先选择服务已配置的设备并连接，再选择动作。「仅编译预览」不创建 Job、不发送设备指令；「播放到设备」才提交带回执的有限任务。页面上的两颗灯显示服务返回的 USB RGB 读回，不是摄像头画面或光学校准结果。

页面内部使用同一个 `light` 对象：`set`、`off`、`fade`、`blink`、`breathe`、`pulse`、`alternate`、`rainbow` 和 `run`。它是本页的交互桥接层，不是已经发布的独立 JavaScript SDK。艺术动作在 `internal/lightaction` 的 Go 核心中编译，浏览器不自行生成它们的 Program。

## 当前双语安装与历史单语版本

历史安装1e固定英文Web cf5219，11文件/210608字节、9公开资源。已安装6ca固定双语Web1e1b1dd，13文件/260151字节、11公开资源；额外两个GET仅为同源语言脚本，不增加硬件操作。三页首次默认英文，可显式切换中文；仅用户选择时写本浏览器的命名空间偏好，跨页/刷新保留，不按OS语言自动切换。语言切换不改变表单、原Record/source/recipe/票据，也不触发预览、保存、播放或取消。

## 高级图案书：历史96a整合候选

候选96a固定Web来源`f90ef8d6143acd55c059b94ecf961ec65a1bb646`；扩展bundle为9个公开页面资源+LICENSE，manifest另计，共208132字节、4合同。`/demo/patterns.html`、`patterns.js`、`patterns.css`已随该构建嵌入并返回200；历史host56c与基础fe32507的三个404保留，合法基础bundle没有这些页面时仍应404。部署状态与本机MK2有限证据看[手册第1节](api-reference.zh-CN.md)，历史96a曾安装并通过33项只读复核；历史1e另有38项证据，当前6ca另有57项只读后验及4项只读UI证据；是否宿主注册/OS服务安装仍须分别核对。

打开`/demo/patterns.html`后，可按图案书里的三步操作：

1. 选Pulse或Orbit例子，编辑ID/标题/source，先预览完整候选。预览保留高级意图、规范动作与冻结执行，既不保存也不播放。
2. 核对完整revision，勾选确认后保存一次；这是电脑独立v2库，不是硬件Flash。保存未知时保留原ID/revision与完整记录，只读核对，不自动重写。完整性诊断不会被随后读到同版记录抹掉。
3. 明确选择设备、版本和policy，先准备并保留原票据，再播放一次。运行中查停只用该Ref/票据；取消先查原回执、再最多一次confirmed_job_id POST。等待超时不等于取消；终态hold再取消不自动关灯。

库含合法未知来源/recipe时只读展示，不把冻结程序降成旧Program补播。离线`file://`设计预览使用明确标注的模拟资料，写入与播放禁用；同源在线页失败不偷偷切模拟、USB或其他daemon。会话恢复资料与显式导出用于保留原请求，不是后台自动重播。

## 版本化动作 API

预览请求为 `POST /v1/light/preview`：

```json
{
  "contract": "nb1-light-action/v1",
  "serial": "YOUR_SERIAL",
  "action": {
    "kind": "breathe",
    "color": "lime",
    "period_ms": 2000,
    "cycles": 3,
    "on_finish": "off"
  }
}
```

`serial` 可省略，此时只做纯编译；提供时只读取已配置设备的缓存状态进行能力检查。响应的 `result` 是动作规范化与编译结果，`preview` 是现有 Program 预览合同。两者应包含同一份规范 Program、步骤数和名义时长。

执行使用 `POST /v1/devices/{serial}/light`，保留 `contract/action`，增加 `policy:"reject"`、`request_id` 和 `expected_daemon_instance_id`，不再在请求体放 `serial`。先调用 `GET /v1/instance`，按照其 `request_id_prefix` 加 32 位随机小写十六进制后缀生成一次逻辑请求的标识；实例标识采用该响应的 `daemon_instance_id`。

执行返回原有 Job JSON，以及实例、重放状态和原回执位置响应头。服务不回退到无回执提交，不替换其他任务。请求结果未知时，页面查询原设备、原请求标识和原实例的回执，不换键重发。

`light.run` 接受精确 Program，使用已有 `/v1/programs/preview` 和设备 `/jobs` 入口；它同样使用完整回执及 reject 策略。旧 Effect v1 和 MCP 工具结构保持原义。

## 结束与停止

基础 `set/fade` 默认保持最终颜色；艺术动作默认完成后熄灭。这里的新 `pulse` 是快起、衰减、停顿；旧 Effect v1 `pulse` 的对称呼吸语义不变。

「停止本次任务，并熄灭」只取消本页已确认的 Job，等该 Job 进入终态后，再提交明确的 `off`。它不会根据设备的任意 `active_job` 去取消别人的任务。结果仍未知时先查原回执；设备被其他客户端占用时，新的 off 也遵守 reject。

页面关闭不会可靠地发送停止指令；任务仍由本机服务完成。预设动作均为有限循环，结束行为会显示在编译结果中。

两颗 LED 的 `alternate` 表达颜色交换，不承诺可辨的顺时针／逆时针旋转。页面不把虚拟屏幕效果冒充硬件或摄像头验收。
