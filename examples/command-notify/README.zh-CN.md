# 命令完成后，用 blink(1) 通知

这个 Python 3 标准库示例包装现有 `nb1` CLI：命令成功显示绿色，失败显示红色，默认显示 1.5 秒，最后熄灯。可用于构建、测试或 Agent 任务结束提醒。使用者必须明确选择 daemon 已配置的设备。

示例不实现 HTTP 客户端、不直连 USB，也不安装服务。底层执行仍由现有 daemon、Program 和实例绑定回执负责。

## 先预览，再执行

准备好 Python 3.8+、当前候选版 `nb1` 和已经运行的本地 daemon。旧版 CLI 或 daemon 可能没有 `control prepare`，会停止通知，不回退到无回执或直控。

默认是 dry-run：**既不执行用户命令，也不调用 nb1**，只输出命令 argv 和成功/失败两种 Program。

```sh
python3 examples/command-notify/notify.py --serial 20005601 -- go test ./...
```

明确加 `--execute` 才执行命令并发送灯光通知。每次执行需要一个**尚不存在**的报告文件，避免覆盖上一任务的未知回执：

```sh
python3 examples/command-notify/notify.py \
  --execute --nb1-binary /absolute/path/to/nb1 \
  --endpoint http://127.0.0.1:8931 --serial 20005601 \
  --report ./build-notify-001.json -- go test ./...
```

Windows PowerShell 中使用当前安装的 Python 命令和 `.exe` 路径，例如：

```powershell
py -3 examples/command-notify/notify.py --execute --nb1-binary C:\tools\nb1.exe --serial 20005601 --report .\build-notify-001.json -- go test ./...
```

`--` 后是可执行文件及其逐项参数，按 argv 传给 `subprocess`，没有 `eval` 或隐式 shell。管道、重定向和 `&&` 不会自行展开；需要脚本时直接调用相应脚本解释器。子命令 stdout/stderr 原样保留；最终结果摘要写到 stderr，完整 JSON 写入 `--report`。

## 一个 Program，一次提交

命令结束后，示例调用一次 `control prepare`，保存原 `request_id`、`expected_daemon_instance_id`、设备、策略和完整 Program，再调用一次：

```text
nb1 control --mode daemon --endpoint <origin> --timeout <remaining>s 
  run <serial> <temporary-program.json> --policy reject
  --request-id <original-request> --expected-daemon-instance-id <original-instance> --wait
```

Program 是“指定颜色 → 短时停留 → 黑色”，并带 `repeat: 1`、`on_finish: off`。熄灯属于同一个任务，不再追加第二个设色请求。`--wait` 由现有 CLI 使用原回执查询终态。成功/失败颜色可通过 `--success-color '#00a000'`、`--failure-color '#b40000'` 修改；`--duration-ms` 接受 10–10000 毫秒、10 毫秒的整数倍。

默认 `--policy reject`，设备已有任务时不打断。`queue` 或 `replace` 必须显式指定；排队可能超过通知等待预算。`--notify-timeout` 默认 10 秒，覆盖 prepare、提交和等待，不能强制终止 daemon 或正在执行的原生 USB 调用。

## 命令结果与通知结果分开

包装器的退出码保留子命令结果：成功为 0，失败保留原码，无法启动为 127；POSIX 信号退出转换为 `128 + signal`。灯通知失败不把成功构建改为失败，也不把失败构建改为成功。参数或报告创建失败时返回 2，且命令尚未执行。需要强制检查灯通知的流水线应另读报告：

| 字段 | 含义 |
| --- | --- |
| `command.exit_code` | 子命令原始退出码；POSIX 信号在这里仍是负数 |
| `notification.state` | `not_submitted` / `prepared` / `unknown` / `succeeded` / `failed` |
| `notification.outcome_unknown` | 是否无法确认已尝试提交的最终结果 |
| `notification.final_off` | 仅匹配 Program 的成功终态标记 `daemon_job_confirmed`；其余为 `not_confirmed` |
| `notification.binding` | 原实例与原 request ID，提交前已保存 |
| `notification.receipt_lookup_argv` | 使用原二元组只读查询的完整 argv |

超时、响应丢失、CLI 结果不能解码或实例改变时，示例不重试 POST、不重新 prepare、不换键补发，也不会另发“清理”请求。原任务可能仍在运行，不能承诺此时灯已经熄灭。报告保存了同一 Program，供人工判断和后续查询；不要把 `request_not_found` 当作原任务未执行的证明。

`daemon_job_confirmed` 表示当前 daemon 已确认该任务与其结束清理成功，仍是软件/读回证据，不是摄像头或未来不会被其他任务改色的保证。回执保存在 daemon 内存中，不保证跨重启去重。中断包装器不等于取消已提交任务；命令执行阶段收到 Ctrl+C 时跳过通知。

## 支持与验证边界

代码只使用 Python 标准库，按跨平台 argv、临时文件关闭后再交给 CLI、原子替换报告文件的方式编写。Windows 使用可执行的 `nb1.exe`，macOS/Linux 使用对应平台 CLI；不依赖 shell 脚本、Node、外部 Python 包或 Windows SCM。

软件契约测试覆盖成功与失败命令、原样 argv、prepare 失败、通知超时/结果未知不重复提交、终态失败不夸大熄灯、报告持久化失败不提交，以及旧报告不覆盖：

```sh
python3 -m unittest discover -s examples/command-notify -p 'test_*.py' -v
```

如已有当前 `nb1` 二进制，可设置 `NB1_NOTIFY_TEST_BINARY` 指向它，再运行相同测试命令；额外三项本地 HTTP fixture 测试会调用真实 CLI，验证命令成功/失败、实例绑定、响应丢失和超时只提交一次。未设置时这三项明确跳过。fixture 不连接 USB，也不启动真实 daemon。

本轮验证环境为当前 macOS；fake CLI 与真实 CLI 的本地 HTTP fixture 验证仅证明软件约定。Windows/Linux 上机运行、各系统 USB 实物、旧 OS、mk3 和真实拔插仍分别待验。包装器不会扩大底层 SDK/GID 的已验证支持范围。
