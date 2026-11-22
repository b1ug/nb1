# Playback 策略

Playback 策略决定 daemon 是否接收新的灯光 Job。HTTP、MCP 和连接 daemon 的 CLI 共用同一规则，覆盖 Action、Sequence、Pattern 和 Trigger。Preview、状态查询、取消和其他管理请求在 Playback 被抑制时仍可使用。

`nb1 control --direct run` 会在本次调用中读取配置的持久策略，但看不到 daemon 内存中的临时覆盖和历史记录。直接打开 USB 的旧式顶层命令不经过 daemon 准入流程。需要让临时覆盖和请求记录在多个客户端间共享时，请使用 daemon 接口。

默认模式为 `always`，保持现有行为。daemon 在启动时读取持久配置；修改 `config.yaml` 后需要重启 daemon。

## 持久配置

```yaml
playback:
  mode: always       # always | weekly | never
  timezone: local    # local 或 IANA 时区，例如 Asia/Shanghai
  history_limit: 100 # 内存中的策略决策条数，范围 1–10000
  weekly: {}
```

`weekly` 只允许表中列出的时段。未列出的日期和时间都会被抑制。每个星期几最多配置一个允许时段，使用 24 小时制 `HH:MM`；开始时刻包含在内，结束时刻不包含。
`local` 使用机器本地时区；IANA 时区由 Go 内嵌的时区数据库提供后备数据，使同一配置可在 macOS、Linux、Windows 上使用，无需额外模块。

```yaml
playback:
  mode: weekly
  timezone: Asia/Shanghai
  history_limit: 100
  weekly:
    mon: {start: "08:00", end: "20:00"}
    tue: {start: "08:00", end: "20:00"}
    wed: {start: "08:00", end: "20:00"}
    thu: {start: "08:00", end: "20:00"}
    fri: {start: "08:00", end: "20:00"}
```

跨午夜必须显式写 `overnight: true`：

```yaml
playback:
  mode: weekly
  timezone: local
  weekly:
    fri:
      start: "22:00"
      end: "02:00"
      overnight: true
```

周五这段时段也会允许周六凌晨 00:00 到 02:00。周六自己的规则独立生效。开始和结束相同是无效配置；全天禁止播放用 `never`，全天允许用 `always`。

## 临时覆盖

daemon 同时只保存一个临时覆盖：

```sh
nb1 playback status
nb1 playback override suppress --for 1h
nb1 playback override permit --for 15m
nb1 playback override clear
nb1 playback requests --limit 20
```

`suppress` 会忽略持久模式并抑制新的播放请求；`permit` 会忽略持久模式并允许播放。新覆盖会替换旧覆盖。`clear` 立即恢复持久策略。覆盖会自动到期，最长 720 小时；daemon 重启后覆盖消失。

## HTTP 与 MCP

| 操作 | 行为 |
| --- | --- |
| `GET /v1/playback` | 查看持久模式、有效的 `allowed` / `suppressed` 状态、原因和当前覆盖。 |
| `GET /v1/playback/requests?limit=20` | 查看最新策略决策，最多返回配置的历史条数。 |
| `POST /v1/playback/override` | 请求体：`{"mode":"suppress","duration_ms":3600000}` 或 `{"mode":"permit","duration_ms":900000}`。 |
| `DELETE /v1/playback/override` | 清除临时覆盖。 |

MCP 提供对应的 `get_playback_status`、`list_playback_requests`、`set_playback_override` 和 `clear_playback_override` 工具。远程 HTTP/MCP 继续使用现有 HTTPS Bearer 鉴权；本机 API 沿用现有可选 Token 设置。

被策略挡下的播放请求返回 HTTP `423` 和错误码 `playback_suppressed`。它不会创建 Job、进入设备队列或发送硬件指令；策略稍后变为允许时也不会自动重放。恢复允许后，调用方可以用新请求重试。

有界历史只记录时间、决策、原因、请求类别、目标设备和调用方提供的请求/Job ID，不保存 Action、Sequence、Pattern 内容或用户文本。历史仅在内存中，默认 100 条，最新记录在前；daemon 重启后清空。历史记录表示策略决策，不代表灯的实体输出已成功。

## 边界

`always`、`weekly` 和 `never` 是 Playback 模式。本功能不读取 macOS Focus、Windows 静默时段或 Linux 桌面通知设置。策略只决定是否接收新任务；取消、Job 查询和诊断接口仍可用。周计划结束或覆盖切换时，已经接收的 Job 不会自动停止。
