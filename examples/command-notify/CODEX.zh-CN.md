# Codex 回合通知

[English](CODEX.md) · [普通命令包装器](README.zh-CN.md) · [验证记录](VERIFICATION.md)

这组示例通过已有 nb1 daemon 播放一个有限的蓝色 blink：亮 1.5 秒，再由同一个 Job
熄灯。蓝色表示**回合结束**，事件本身不能判断用户任务是否成功。

根据事件来源选择入口：

| 来源 | 脚本 | 输入 | 成功时 stdout |
| --- | --- | --- | --- |
| Codex 外部 `notify` 命令 | `codex_notify.py` | Codex 追加的单个 JSON 参数；含 `type: "agent-turn-complete"`、`thread-id`、`turn-id`。 | 空。 |
| Codex lifecycle Stop hook | `codex_stop.py` | stdin JSON；含 `hook_event_name: "Stop"`、`session_id`、`turn_id`、`stop_hook_active: false`。 | `{}`。 |

请将脚本与共用的 `notify.py` helper 放在一起。[README](README.zh-CN.md)里的普通
命令包装器接收命令 argv，不是这两个 Codex 事件接收器。两个事件脚本默认只预览，
显式加 `--execute` 才调用 nb1。

## 外部 notify：先预览，再选择单次接入

官方[通知说明](https://learn.chatgpt.com/docs/config-file/config-advanced#notifications)
定义了命令数组与 Codex 传入的单个 JSON 参数。先预览一个合成事件，无需 daemon 或硬件：

```sh
python3 examples/command-notify/codex_notify.py \
  '{"type":"agent-turn-complete","thread-id":"demo-thread","turn-id":"demo-turn"}'
```

预览只向 stderr 写简短说明，stdout 为空，不创建报告或 Job。非目标、畸形、超限或
信息不完整的事件会被忽略。目标事件的 thread／turn ID 必须非空，各不超过 256 个
UTF-8 字节；整个 JSON 参数不超过 256 KiB。

执行前准备 Python 3.8+、同版 nb1 程序、已配置目标设备的本机 daemon，以及已存在的
私有报告目录。替换下面所有路径、地址与序列号。程序和脚本使用绝对路径；
`DEVICE_SERIAL` 必须是该 daemon 已配置的设备。

```sh
codex exec \
  --ignore-user-config --ephemeral \
  --skip-git-repo-check --sandbox read-only \
  --cd /absolute/path/to/empty-workdir \
  -c 'notify=["/absolute/path/to/python3","/absolute/path/to/command-notify/codex_notify.py","--execute","--nb1-binary","/absolute/path/to/nb1","--endpoint","http://127.0.0.1:18000","--serial","DEVICE_SERIAL","--report-dir","/absolute/path/to/private-reports"]' \
  'Reply exactly NB1_NOTIFY_DONE. Do not use tools.'
```

不要把上面的示例 JSON 加进命令数组，Codex 会追加真实事件。去掉数组中的
`--execute` 即可只做预览。启动或安装 daemon 是另一项明确选择；脚本不会执行这两件
事，也不静默切换为 USB 直连。

此命令只覆盖本次调用的配置。项目中的 `.codex/config.toml` 不能提供 `notify`，详见
官方[配置参考](https://learn.chatgpt.com/docs/config-file/config-reference)。已安装的
`codex-cli 0.166.0` 公开 `exec --help` 确认支持以上选项：`--ignore-user-config`
不加载用户 `config.toml`，认证仍由原 Codex 安装处理；`--ephemeral` 不持久保存会话
文件。read-only 沙盒约束模型生成的命令，明确指定的通知脚本仍是宿主机程序。
本示例不修改全局配置，不改变 `HOME`／`CODEX_HOME`，不绕过 hook 信任；既有管理策略
仍可能生效。需要时选用当前安装可用的模型，示例不固定模型名。

## 报告与恢复

外部 notify 执行模式要求 `--nb1-binary`、`--endpoint`、`--serial` 和 `--report-dir`。
程序必须以绝对路径指定、已存在且可执行。既有报告目录必须是绝对路径，本身不能是
符号链接。POSIX 上要求归当前用户所有，且没有 group／other 权限位，例如 `0700`；
不合适时脚本会拒绝，不会自行修改目录权限。Windows 上由调用者用 ACL 保持目录私有。
endpoint 必须是显式带端口的 HTTP 回环地址。脚本固定使用四秒提交／等待预算；超时
不取消已经接受的 Job。

报告使用 `schema_version: 2`、`trigger: "codex_notify"`、`meaning: "turn_ended"`。
文件名为 `codex-notify-<hash>.json`，hash 是 thread／turn ID 的 SHA-256。报告采用
排他创建，同一事件再次到达时保留原报告，不增加请求。报告不保存消息正文、cwd、
transcript 路径或原始事件 ID；只保留目标地址、序列号、Action、原始短 Job ID 与
查询 argv。报告目录的保留与清理由调用者管理；需要抑制重复投递时，应保留对应报告，
删除报告会移除这个事件的去重门槛。

在唯一一次 `control run SERIAL FILE --id ID --wait` 前，helper 先保存
`notification.job_id`、`notification.input` 和 `notification.job_lookup_argv`，
最多发一个 POST。设备忙／离线、响应丢失、CLI 输出无效或超时，都不触发重发、
直连回退或自动取消。用报告里的查询 argv，在**原地址查询原 Job ID**。

只有 CLI 确认 ID、serial 匹配的成功终态，报告才标记
`notification.state: "succeeded"` 与 `final_off: "daemon_job_confirmed"`。
未知结果仍保持未知；记录因到期、淘汰或重启而不存在，不能证明从未播放。
软件终态也不等于光学验收。

| 适配器结果 | 退出码 | 含义 |
| --- | --- | --- |
| 预览或忽略事件 | `0` | 没有请求播放。 |
| 有效事件与通知尝试 | `0` | 应检查报告；退出码不证明灯光成功，操作失败不会请求继续回合。 |
| 命令行参数无效 | `1` | 应修正明确指定的通知配置。 |

事件处理的 stdout 为空，`--help` 正常输出帮助。适配器与 Codex 主进程有各自的退出码。
已记录的 `codex-cli 0.166.0` 验收确认：真实通知成功与 daemon 不可达时，Codex 均返回
`0`。在目标安装上仍应核对投递与宿主处理通知失败的方式。证据见 [VERIFICATION.md](VERIFICATION.md)。

## Lifecycle Stop 是另一个入口

用 stdin JSON 预览既有 Stop 接收器：

```sh
printf '%s' '{"hook_event_name":"Stop","session_id":"demo-session","turn_id":"demo-turn","stop_hook_active":false}' \
  | python3 examples/command-notify/codex_stop.py
```

需要选择 hook 接入时，先检查 [codex-hooks.example.json](codex-hooks.example.json)，
替换路径、endpoint 与 serial，并准备私有报告目录。它只是 Codex 配置目录之外的
示例文件；安装时使用正常的 hook 评审与信任流程。详见官方 [Hooks 指南](https://learn.chatgpt.com/docs/hooks)。

Stop 接收另一种事件，成功时向 stdout 返回 `{}`。参数错误返回 `1`，避免使用具有
Stop 继续执行语义的退出码 `2`。它忽略 continuation 事件，将 session／turn ID
哈希为 `codex-stop-<hash>.json`。有限蓝色 Action、四秒预算、排他报告去重与原 Job
恢复规则与前面相同。不要将 `codex_stop.py` 放进外部 `notify` 数组，也不要向它传入
notify JSON。

合成测试、真实外部 notify 投递、已安装的 Stop hook 与实体灯光需要分别验证。
测试命令见 [README](README.zh-CN.md)，历史验证记录保留原日期、源码与范围。

2026-11-13 的软件验收通过 23 项测试与四次真实 Codex 回合。最终成功回合经真实 Go
内存执行器完成一个 Job，结束为黑色；额外显式投递的合成重复事件没有增加 POST。
最终不可达回合保留 unknown 结果，Codex 退出码保持 `0`。成功案例中观察到通知先于
CLI 退出完成，这不是通用的生命周期时序保证。这些回合没有验收实体设备、全局
hook 安装或 Windows。详见[完整验收记录](VERIFICATION.md#real-codex-notify-acceptance--2026-11-13)。
