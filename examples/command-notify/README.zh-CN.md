# 命令完成后，用 blink(1) 通知

[English](README.md)

这个 Python 3 标准库示例调用正式 `nb1 control run`：命令成功显示绿色，失败显示红色，默认停留 1.5 秒后熄灯。灯光由 daemon 编译 Action、执行 Sequence 并发布 Job 终态。示例不直连 USB、不安装服务。

## 预览与执行

准备 Python 3.8+、支持 Action／Sequence／Job API 的 nb1 和已配置设备的本地 daemon。默认 dry-run 既不执行用户命令，也不调用 nb1，只显示 argv 和两种 Action 输入：

```sh
python3 examples/command-notify/notify.py --serial 20005601 -- go test ./...
```

明确加 `--execute` 才执行命令并通知。每次使用尚不存在的报告文件：

```sh
python3 examples/command-notify/notify.py \
  --execute --nb1-binary /absolute/path/to/nb1 \
  --endpoint http://127.0.0.1:18000 --serial 20005601 \
  --report ./build-notify-001.json -- go test ./...
```

示例 endpoint 需替换为使用者自己的本地 daemon 地址。Windows PowerShell 可使用 `py -3` 和对应 `nb1.exe` 路径。`--` 后的参数原样传给 subprocess，使用 `shell=False`，不展开管道、重定向或 shell 表达式。子命令输出保留；通知摘要写 stderr，完整报告写入 JSON。

## 一个 Action、一个 Job、一次提交

通知输入使用一个有限 `blink` Action：

```json
{"action":{"kind":"blink","led":0,"color":"#00a000","on_ms":1500,"off_ms":10,"cycles":1,"on_finish":"off"}}
```

它停留指定颜色、停留黑色 10ms，再由同一个 Job 完成结束清理。示例先生成并持久保存 Job ID、原输入和查询 argv，再调用一次：

```text
nb1 control --endpoint <origin> --timeout <remaining>s
  run <serial> <temporary-action.json> --id <saved-id> --wait
```

CLI 只向 `POST /v1/jobs` 提交一次，之后用 `GET /v1/jobs/<id>` 等待终态。设备忙时由正式入口拒绝新工作，不抢占现有任务。成功／失败颜色可用 `--success-color`、`--failure-color` 修改；`--duration-ms` 接受 10–10000ms、10ms 的整数倍。`--notify-timeout` 默认 10 秒，覆盖提交与等待；超时不会取消 daemon 中的任务。

报告格式为 schema_version 2：

| 字段 | 含义 |
| --- | --- |
| `command.exit_code` | 原始子命令退出码；POSIX 信号在这里仍为负数 |
| `notification.input` | 提交的原始 Action 封套 |
| `notification.job_id` | 提交前已保存的唯一 Job ID |
| `notification.job_lookup_argv` | `control job ID` 的只读查询 argv |
| `notification.state` | `not_submitted`／`unknown`／`succeeded`／`failed`／`cancelled` |
| `notification.outcome_unknown` | 尚未确认提交尝试的结果 |
| `notification.final_off` | 仅匹配 ID、serial 的成功 CLI 终态标为 `daemon_job_confirmed` |

通知故障不改变子命令结果：成功保留 0、失败保留原码、启动失败为 127；POSIX 信号映射为 `128 + signal`。参数或初次报告创建失败时返回 2，命令尚未开始。命令阶段收到 Ctrl+C 时跳过通知。

响应丢失、超时、拒绝或无效 CLI 输出均不自动重放；无法从输出确认结果时保守记为 unknown。使用报告保存的 `job_lookup_argv` 查询同一个 Job。确需取消时显式执行 `nb1 control --endpoint <origin> cancel <saved-id> --wait`。包装器不自动补发取消或熄灯请求。

`daemon_job_confirmed` 是 daemon 的完成／清理证据，不是摄像头验收。Job 保存在内存中，重启或保留期结束可能使其不可查询；查询不到不能证明从未执行，也不能据此自动生成新 ID 重播。

## 软件验证与 Stop 示例

```sh
NB1_NOTIFY_TEST_BINARY=/absolute/path/to/current/nb1 \
  python3 -m unittest discover -s examples/command-notify -p 'test_*.py' -v
```

14 项测试包含控制流、当前真实 CLI＋临时端口 synthetic HTTP、合成 Stop JSON。覆盖退出码、单次 POST、有限 Action、Job 终态、响应丢失后原 ID 查询、超时／忙／离线／无效响应、报告保存失败与重复事件。未设置二进制时，真实 CLI 的测试明确跳过。

[Codex Stop 示例](CODEX.md) 复用相同通知实现，默认仅预览。synthetic 测试不等于 live hook：没有启动真实 Codex 回合、修改用户 hook 配置或验证灯具联动。当前软件验证也不替代其他 OS、旧系统、USB 型号或 MK3 的实物验收。
