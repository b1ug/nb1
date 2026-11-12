# Daemon 验证与平台边界

[English](daemon-validation.md)

这是开发候选的 2026-11-12 验证快照，不代表已经发版或所有系统的实体 USB 都通过验收。

## 一个程序，显式选择模式

`nb1` 同时包含 CLI 与 daemon。`nb1 server --devices SERIAL` 在前台运行；安装可执行文件不会自动安装或启动服务。Mac/Linux 的 `nb1 service install --devices SERIAL` 安装当前用户的登录服务，默认不立即启动；加 `--start` 或随后执行 `nb1 service start` 才启动。省略 `--binary` 时使用当前可执行文件。Windows 自动服务安装尚未实现。

`nb1 control ...` 使用所选的本机 HTTP 地址。daemon 不在时明确报错，由用户选择启动或显式直连；不会自动启动，也不会静默切换 USB。`nb1 control --direct run SERIAL FILE` 用于不依赖 daemon 的前台播放，并与 daemon 共用原生设备锁。

## 已取得的证据

| 目标 | 实际前台 daemon / CLI / 内嵌 Web | 用户服务 |
| --- | --- | --- |
| Mac / Apple Silicon | 原生 Actions：16 个功能检查及清理通过 | 本机 macOS 27.2：18 个 LaunchAgent 生命周期检查通过 |
| Mac / Intel | 原生 Actions：16 个功能检查及清理通过 | 未在 Intel 实机验收 LaunchAgent |
| Linux / amd64 | 原生 Actions：16 个功能检查及清理通过 | Ubuntu 24.04 的真实用户 manager：Homebrew 安装、升级、回退及清理通过 |
| Linux / arm64 | 原生 Actions：16 个功能检查及清理通过 | Ubuntu 24.04.5 VM：33 项，含重启后用户登录自动启动与本地库保留 |
| Windows / amd64 | 原生 Actions：16 个功能检查及清理通过，含定向 CTRL_BREAK 退出 | 仅 render 与只读 status；自动安装、启停、卸载尚未实现 |

[五平台实际候选验收](https://github.com/b1ug/nb1/actions/runs/43186037056)对应 `45d92e75bd68e92f815aab3157d3d5b6c34d7f92`：每个平台编译干净来源的原生 CGO 程序，校验提交与依赖，再运行同一个二进制。80 个功能检查及五份清理结果全部通过，无强制终止。云端明确要求没有接入 blink(1)。

[Linux Homebrew 联合验收](https://github.com/b1ug/nb1/actions/runs/43182659126)对应 `b3b5ac27e7ae7f66652540bf5a9d1b49fec1aec9`：79 条生命周期及拒绝测试记录、六类预期拒绝、19 个升级检查，以及临时服务、包、tap、账号清理通过。使用隔离的开发 tap，没有公开发版。后续候选只调整测试样本和验证工具，daemon 执行代码与该来源相同。

前台检查覆盖明确不存在的设备、HTTP 健康、CLI 状态、纯预览、离线拒绝执行、内嵌 HTML/JS/字体、冲突端口、外来 Origin 拒绝、换路径重启后的颜色和 Pattern 保留、正常信号退出，以及连接失败不自启。详见[验收工具](../ci/README.daemon-service.md)。

## 安装与持久化

Linux VM 的 `Linger` 始终为 `no`；确认系统重启后，首次用户会话触发已启用的服务恢复，没有再次调用启动命令。因此它证明登录后自动启动，不证明无人登录时启动。产品命令由普通用户执行，无实体 USB 透传。测试后服务、VM 和任务临时状态已删除。

安装会检查程序与配置路径的祖先目录。VM 测试先创建了组可写的 `~/.local`，产品正确拒绝安装；只收紧测试用户自己新建的目录后通过。安装器不会擅自修改祖先权限。真实环境遇到这一错误时，应检查报错的目录并选择可信安装路径，不要递归修改已有共享目录权限。

颜色与 Pattern 保存在磁盘。Job 状态与去重记录属于单个 daemon 进程，重启清空；保留 Pattern 不会导致重启后自动播放。包升级后需要显式重启服务进入新版本；Homebrew 验收核对稳定入口，避免绑定会被清理的旧 Cellar 版本路径。

## 尚未验收

Linux/Windows 实体 USB、MK3、Flash、拔插恢复和无限期稳定性均不在本轮结论中。实体硬件证据仍限 Mac/MK2/FW204。无设备的信号退出不能证明阻塞的原生 USB 调用可以强制中断；客户端和停机等待预算不等于原生 I/O 硬截止。尽力输出的停机日志未出现时，必须标记未观测，不能把它当作硬件清理成功。
