# 本地实灯工作台

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

前两条只读。导入接受两个完整、闭合的产物组合：基础组合为 `index.html`、`demo.css`、`demo.js`、`orbit.html`、`orbit.css`、`orbit.js`、`LICENSE.txt`；扩展组合在此基础上要求全部三个 `patterns.html/js/css`。基础组合必须声明原来的两项 light 合同，扩展组合还必须声明 `nb1-pattern-library/experimental-v2` 与 `nb1-pattern-record/experimental-v2`。不接受只加入一两项新资源或合同，也不接受用已知新资源替换旧文件。

整个 dist 含 manifest 仍至多 256 KiB。导入检查来源、原 manifest 字节、逐文件大小和摘要，并按 ASCII 路径排序后将 `path + NUL + sha256 + LF` 拼接为 UTF-8，重算七项或十项资源的 `source.tree_sha256`。dirty 来源、未知文件、重复字段、null、符号链接、重解析点与硬链接均拒绝。独立 Web 仓库的源码提交和锁文件仍需由构建方审查；导入器验证的是明确指定的产物，不替代来源可信度判断。

可在明确重新指定旧来源及摘要后导入完整基础组合用于恢复。有效基础组合没有新页面时，其三个新资源返回404；必需资源缺失或摘要不符仍视为整个bundle校验失败，不以404掩盖。新增页面沿用同源、只允许GET、无query/body、no-store/nosniff、严格CSP与no-referrer。是否实际嵌入新页面，以本二进制的固定dist为准；单独升级host代码不构成新Web功能验收。

导入在临时目录复制校验后更换生成目录和锁；普通错误会恢复旧版。它不承诺两个路径在崩溃时原子切换，不支持与 Go 构建并发执行。中断残留或不一致的生成目录会拒绝继续；Go 在读取任何页面前还会校验完整的嵌入文件集与锁，失败时不提供页面。`cleanup_warnings` 非空表示新产物可能已安装，但残留需检查，不能只凭命令退出码推断旧产物仍在。

导入后运行 `go test ./internal/webui ./internal/httpapi`。浏览器验证与硬件验收分别记录；一次构建的来源由 Web commit、manifest SHA、nb1 commit 和最终二进制 SHA 共同描述。本地导入不上传、不创建 Release，也不修改系统服务。

先选择服务已配置的设备并连接，再选择动作。「仅编译预览」不创建 Job、不发送设备指令；「播放到设备」才提交带回执的有限任务。页面上的两颗灯显示服务返回的 USB RGB 读回，不是摄像头画面或光学校准结果。

页面内部使用同一个 `light` 对象：`set`、`off`、`fade`、`blink`、`breathe`、`pulse`、`alternate`、`rainbow` 和 `run`。它是本页的交互桥接层，不是已经发布的独立 JavaScript SDK。艺术动作在 `internal/lightaction` 的 Go 核心中编译，浏览器不自行生成它们的 Program。

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
