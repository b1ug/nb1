# nb1

[English](README.md)

通过 CLI、本机 HTTP、MCP 或内嵌 Web 控制 blink(1)。高级 Action 在 nb1 内编译为低级 Sequence，并交给同一设备 worker 执行。

本分支是 API v1 开发候选，源码验证与机器当前安装版本分别记录。同一个可执行文件可以作为 CLI，也可以前台运行 daemon：

```sh
nb1 server --devices DEVICE_SERIAL
```

将请求保存在 `action.json`：

```json
{"action":{"kind":"pulse","color":"lime","cycles":2}}
```

在另一个终端调用：

```sh
nb1 control devices
nb1 control preview action.json
nb1 control run DEVICE_SERIAL action.json --wait
```

`control` 默认连接 `http://127.0.0.1:8931`，可通过 `--endpoint` 指定其他本机端口。daemon 持有 USB，其他入口通过 HTTP 与它交互。一台设备由一个进程拥有。独立的 `on`、`off`、`blink`、`fade`、`play`、`read`、`write` 命令直接访问硬件，需要设备可用。

`server` 只是启动进程，不自动安装开机服务。可以前台运行、作为普通后台进程管理，也可显式选择相应平台的用户服务模式，操作见 `nb1 service --help`。控制灯不需要键盘或输入监控权限。

macOS/Linux 候选支持用 `nb1 service install --devices DEVICE_SERIAL --start` 显式安装登录服务，默认使用正在运行的 nb1；需要选择另一份程序时提供 `--binary /absolute/path/to/nb1`。软件包升级迁移及 Windows 服务管理仍待分别推进。

## 文档与入口

- [API v1](docs/api-v1.md)：Action、Sequence、Job、颜色、本机 Pattern 和资源限制。
- [统一术语](docs/api-v1-terminology.md)：各入口共用的概念。
- [MCP](adapters/mcp/README.md)：真实 stdio 到 HTTP，包含 12 个工具，不包装 CLI，不另占 USB。
- [Agent Skill](skills/blink1-control/SKILL.md) 与 [llms.txt](llms.txt)：Agent 的选择、跟踪和恢复指南。

Web 位于 `/demo/`，默认英文，可切中文。其源码在独立的 nb1-web 私有仓库维护，固定构建结果内嵌到 nb1。官网文档种子在 nb1-site 私有仓库。仅打开网页或预览不会播放。

九个动作分别为 `set`、`off`、`fade`、`blink`、`breathe`、`pulse`、`alternate`、`orbit`、`rainbow`。颜色接受名字、Hex、RGB8、HSV/HSB 和 `@本机名字`。Pattern 存在电脑上；原生 DeviceSequence 的容量与部署另有能力要求。

核心保持 Go 1.18 下限，MCP 适配器为单独的 Go 1.25 模块。软件、实机 USB、安装和相机光学结果分开记录。Mac/MK2 的有限结果不代表 MK3、Linux/Windows USB 或非易失存储已通过。开发提交不等于 tag 或 Release。
