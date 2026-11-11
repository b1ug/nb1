# Linux 用户服务原生验收工具

2026-11-11 更新：独立CI已执行真实 systemd 用户manager的fixture生命周期与全部6类拒绝检查。当前完整范围与新增真实nb1 server阶段见 [English acceptance guide](README.daemon-service.md)；每次是否通过以对应run的results/verdict和明确清理记录为准。

当前交付状态：**工具已准备，尚未在真实 Linux systemd 上执行**。本机语法、fake 回归和 Go 1.18 fixture 编译不构成 Linux 服务 PASS。主线已整合 Linux 服务工厂和 `NoNewPrivileges=yes` 模板；下一步通过独立 runner job 执行原生验收，接线见 [CI 说明](README.systemd-user-ci.zh-CN.md)。

`systemd-user-smoke.py` 只允许在 `GITHUB_ACTIONS=true`、Linux、PID 1 为 systemd 的环境中显式 `--execute`。建议选择 `ubuntu-24.04` 的完整 GitHub 托管 VM，不能使用 `ubuntu-slim` 容器。GitHub 官方说明了 VM 与容器的区别及 Linux runner 的管理权限：[GitHub 托管 runner](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)。环境不满足时返回非零及 `ENVIRONMENT_UNAVAILABLE`，不跳过为 PASS。

默认只输出 `PREPARED_NOT_RUN`；它不会运行 sudo、systemctl、CLI、fixture 或创建用户：

```sh
python3 ci/systemd-user-smoke.py
```

在上述专用 runner 中，由后续 workflow 显式执行以下步骤。`CGO_ENABLED=0` 的 CLI 足以验证服务管理路径，不验证 HID；fixture 自身只有 Go 标准库，无 USB 代码。

```sh
GOWORK=off CGO_ENABLED=0 go build -o "$RUNNER_TEMP/nb1-systemd-cli" .
GOWORK=off CGO_ENABLED=0 go build -o "$RUNNER_TEMP/nb1-systemd-fixture" ./ci/systemd-user-fixture
python3 ci/systemd-user-smoke.py --execute \
  --binary "$RUNNER_TEMP/nb1-systemd-cli" \
  --fixture "$RUNNER_TEMP/nb1-systemd-fixture" \
  --output "$RUNNER_TEMP/nb1-systemd-evidence"
```

两个参数必须是不同的、已存在的绝对可执行普通文件，不能是 symlink。证据目录必须不存在；工具不会覆盖已有验收记录。后续 workflow 应始终上传 `results.json`，但本提交不创建或触发 workflow。脚本将两个二进制复制到新用户的受控目录，并核对 SHA-256，避免 runner 工作区目录权限影响服务的二进制安全检查。

## 实际执行范围

每次随机生成 `nb1ci-` 加 12 位十六进制的用户名，拒绝预存在用户、组、HOME 和 linger。创建请求前记录 `creation_attempted`、现有 UID/GID 集合，并用唯一 GECOS marker 标记本次账户；显式 `--groups ''` 禁止默认附加组，随后核对实际组集合仅包含自己的主组。账户/GID 基线只留在内存，不导出其他用户信息。空附加组的解析依据为 [shadow 官方 useradd 源码](https://github.com/shadow-maint/shadow/blob/master/src/useradd.c)。

host 权限只用于创建这个账户、复制 fixture/CLI、临时启用它的 linger/user manager，以及 finally 清理。用户 manager 和 runtime 目录由 systemd 的标准模板创建，不手工伪造 `/run/user/UID`：[user@.service 与 user-runtime-dir@.service](https://www.freedesktop.org/software/systemd/man/latest/user@.service.html)。

所有产品 CLI、用户级 systemctl、文件观测和负例注入都通过该专用普通 UID 和 `env -i` 执行。只注入固定的 HOME、USER、LOGNAME、PATH、locale、XDG_RUNTIME_DIR、用户 bus 地址；不继承 runner 的 token、SUDO 或 XDG_CONFIG_HOME。不会操作 runner 原账号的 unit、设备或权限。

正例依次验证：

1. 新用户配置缺失，原生 `show` 确认为 `not-found`。
2. 默认 install 后持久启用，但保持 inactive/dead、MainPID=0；unit 和唯一受控 enable link 的目标、UID、inode、摘要正确。
3. start 后原生 active/running；HTTP `/health` 的 fixture PID 等于 MainPID，`/proc/PID/exe` 为 staged fixture，四项 UID 均为专用普通用户，内核 `NoNewPrivs=1`。
4. 再 start 保持相同 PID 和 `/proc` start_ticks；stop 后原进程确实退出，unit 与 enable link 保留；再 stop 幂等。
5. 再 start，然后 uninstall --stop；原进程退出，unit/link 缺失，原生 manager 返回 not-found。

fixture 仅接受 `server --devices HEX8 --listen LOOPBACK_IP:PORT`，兼容可选绝对 `--lock-dir` 参数但不使用设备或硬件锁。它提供只读健康接口并处理 SIGTERM，正常退出；设备序列号 `00000000` 只是 argv 占位。

原生状态使用选择字段的 `systemctl show --all`；loaded、active、enabled 分开判断，未用人类可读 `status` 文本，也未用 CLI 的“注册成功”替代进程/HTTP 证据：[systemctl 机器属性与 enable/disable 语义](https://www.freedesktop.org/software/systemd/man/latest/systemctl.html)。

## 可选独立负例

每项必须显式指定，可重复传入；未选项在 JSON 中保持 `NOT_RUN`。这些场景的原生可运行性目前仍待真实 runner 验证，失败会保留 FAIL，不能自动当作不支持后跳过。

```sh
# 在上一执行命令上追加所需项；每次使用新的 --output 目录。
--negative-case external-enable
--negative-case dropin
--negative-case mask
--negative-case transient
--negative-case need-reload
--negative-case bus-loss
```

| 场景 | 注入和预期 |
| --- | --- |
| external-enable | 新建另一条明确的 target.wants 链接。uninstall 应拒绝删除 unit，外部链接保留；允许先移除其自己管理的 default.target link，测试不会宣称整个操作零改动。 |
| dropin | 创建并 reload 本测试的 drop-in；start 必须拒绝，外部文件与持久配置不变。 |
| mask | 在更高优先级的用户 `user.control` 目录创建准确的 `/dev/null` mask；确认原生 masked 后验证拒绝启动。 |
| transient | 用普通用户 systemd-run 创建同名、无磁盘配置的 fixture；install 应拒绝，原 transient PID 不变，之后只停止这个测试刚创建的 unit。 |
| need-reload | 只更改现有 unit 的 mtime，保留 marker、完整字节与 inode；确认 NeedDaemonReload=yes 后 start 应拒绝，随后恢复时间并 reload。 |
| bus-loss | 仅对一次 CLI 同时设置无效 XDG_RUNTIME_DIR 和 bus 地址；CLI 必须失败，真实 manager/磁盘保持 inactive/不变。只破坏 bus 地址可能仍通过 systemd/private 连接，不能作为失联证明。 |

用户 `.control` 搜索优先级来自 [systemd.unit](https://www.freedesktop.org/software/systemd/man/latest/systemd.unit.html)；同名 transient 的创建方式来自 [systemd-run](https://www.freedesktop.org/software/systemd/man/latest/systemd-run.html)。查询期间精确改写的确定性竞态仍由服务包 fake/barrier 测试覆盖，不能通过随机 sleep 冒充原生证明。

## 清理、证据及界限

如果 useradd 失败或超时，finally 也会只读核对随机用户名、唯一 marker、此前不存在且当前唯一的普通 UID/GID、准确 HOME 及其属主。能证明是本次新建的账户才进入清理；只剩孤立组/HOME、marker 不符、旧 UID 或无法读取时，明确记录 cleanup FAIL 和最小残留观测，保留资源，不盲删。useradd 超时仍不能证明原生创建已结束；即使当前观测已清理，也保留 cleanup FAIL，防止把潜在延迟写入误报为清理成功。

finally 再次核对新账户的名字、UID/GID、HOME 和 marker。**先完成全部 runuser 文件观测和精确清理，再取消 linger、终止用户并停止它自己的两个模板实例**；之后永久禁止 runuser，避免 PAM 重建会话。只按记录的完整 inode/摘要/target 删除测试 unit、link 和注入文件，遇到变更拒绝 unlink。原生 host 查询确认 manager/runtime inactive 后，才删除核实的新账户及其新 HOME，检查账户、主组、HOME、runtime、linger 和最后一个 fixture 进程均已消失。文件变化时保留账户/HOME 并报告失败。不会调用宽泛 `systemctl disable`、killall 或原 runner 用户的管理命令。即使主流程通过，清理失败也会把总结果降为 FAIL。

证据包含二进制 SHA-256、精选原生属性、HTTP fixture PID、最小 `/proc/status` 字段、文件 inode/摘要/链接目标及清理结果。命令 stderr 只保存长度和 SHA-256，不保存原文；不采环境、token、键盘/USB 信息、journal 或 `/proc/environ`。轮询时间只是观察预算，命令超时不等于原生操作被取消。SIGINT/SIGTERM 会尽力进入清理；SIGKILL 或 VM 强制销毁无法保证 finally 执行，runner 的销毁不能计为清理 PASS。

将来的 PASS 只证明该 runner 上的用户服务生命周期和 NoNewPrivileges 生效；不证明 Linux USB、登录/重启自启动、睡眠恢复、其他发行版或真实 blink(1) 操作成功。

本机允许的软件检查：

```sh
python3 -m unittest discover -s ci -p 'test_systemd_user_smoke.py' -v
python3 -O -m unittest discover -s ci -p 'test_systemd_user_smoke.py' -v
GOWORK=off CGO_ENABLED=0 go test ./ci/systemd-user-fixture
GOWORK=off CGO_ENABLED=0 go vet ./ci/systemd-user-fixture
```
