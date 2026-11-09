# 本地实灯工作台

此页面由 nb1 的 HTTP 服务直接提供：`/demo/`。HTML、CSS 和 JavaScript 嵌入二进制，所有 API 请求使用同一个 origin；不依赖 Node、CDN、外部脚本或云端存储。

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
