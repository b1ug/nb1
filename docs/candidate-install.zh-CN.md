# 试用同版 nb1 候选

[English](candidate-install.md)

本文用于开发候选，不代表已打 tag、正式发版或已有公开安装源。每套产物均标记
`release: false`。首批实机交付范围是已验证的 macOS／Apple Silicon／MK2／FW204
组合。其他平台有归档，不等于其 USB 或托管服务已经验收。详见[平台证据与边界](daemon-validation.zh-CN.md)。

## 运行前核对整套产物

从约定的交付位置取得候选目录。目前没有此候选的公开下载地址或 Homebrew tap。
保留以下五个文件；`<commit12>` 是同一 core commit 的前十二位：

| 文件 | 内容 |
| --- | --- |
| `nb1-<commit12>-<os>-<arch>.tar.gz` | CLI、前台 server 与内嵌 Web；含 `nb1`、`LICENSE`、`build.json`、`cli-help.json`、`SHA256SUMS`。 |
| `nb1-mcp-<commit12>-<os>-<arch>.tar.gz` | 独立 stdio 适配器；含 `nb1-mcp`、`LICENSE`、`build.json`、`README.md`、`SHA256SUMS`。 |
| `blink1-control-<commit12>.zip` | 完整 Skill：`SKILL.md`、`agents/openai.yaml` 与 `references/` 中的三个文件。 |
| `suite.json` | 同版源码、目标平台与产物身份。 |
| `SHA256SUMS` | 整套文件的校验和。 |

系统名使用 `darwin`、`linux`、`windows`，架构名使用 `arm64` 或 `amd64`。
Windows 二进制包使用 `.zip` 和 `.exe`；Skill 与平台无关。这些是打包命名规则，
不代表所有目标都已经提供产物。

在 macOS 的候选目录中执行：

```sh
shasum -a 256 -c SHA256SUMS
```

Linux 可以用 `sha256sum -c SHA256SUMS`。检查 `suite.json` 中的完整源码 commit、
目标平台及 `release: false` 是否符合这次交付。在对应版本的源码目录中，还可运行
离线校验器，检查归档内容及整套身份；它不执行二进制、不访问 USB、不启动 daemon：

```sh
python3 ci/candidate-suite.py verify --directory /absolute/path/to/candidate-directory
```

校验器属于源码仓库，不在二进制包内。校验和能发现不一致，但不是签名，也不能独立
认证来源；文件与校验和被一起替换时仍可能通过。请从可信交付来源取得产物与预期
源码身份。

## 解压到自己的目录

各归档根目录直接放文件，没有外层文件夹。CLI 与 MCP 应分别解压到新的目录，避免
相互覆盖 manifest。以下以首批 macOS arm64 为例；请替换文件名中的 `<commit12>`：

```sh
mkdir -m 700 nb1-cli
tar -xzf "nb1-<commit12>-darwin-arm64.tar.gz" -C nb1-cli
cd nb1-cli
shasum -a 256 -c SHA256SUMS
./nb1 --help
./nb1 version
```

如果之后从这里注册服务，请把目录保留在可信、稳定的位置。以上命令不做全局安装，
不启动服务。核对 `build.json` 与 suite 的源码及平台身份；已有 `version` 显示只是
辅助诊断，不代替整套产物的身份合同。

## 选择设备与配置

试用时显式调用这份候选程序。`./nb1 list` 枚举已连接设备，不会替你选择默认设备。
没有发现设备时，先检查连接，不要虚构序列号。

```sh
./nb1 list
./nb1 config path
./nb1 config init
./nb1 config show
```

`config init` 创建私有模板，已有文件时拒绝覆盖。检查并编辑当前选中的文件。
macOS/Linux 默认为 `~/.config/nb1/config.yaml`；绝对路径的 `XDG_CONFIG_HOME`
可以选择其他根目录。Windows 使用 `%APPDATA%\nb1`。`--config` 与 `NB1_CONFIG`
可以指定文件。详见[配置优先级](api-v1.md#user-configuration)。

例如，把 `20000001` 换成设备真实的八位十六进制序列号，并保留引号：

```yaml
device: "20000001"
server:
  devices: ["20000001"]
  listen: "127.0.0.1:8931"
jobs:
  terminal_limit: 16384
  terminal_ttl: 30m
library_dir: ""
```

空的 library 路径会将本机颜色与 Pattern 保留在应用配置根目录下。macOS 设备所有权
锁仍使用 `~/Library/Application Support/nb1/device-locks`。单独换一个 `--config`
文件，不会移动锁目录或默认作品库。同一设备的所有控制进程应使用同一锁位置。
既有数据不会自动迁移。

## 选择前台 server 或显式直连

需要让 CLI、Web、MCP 共用设备时，启动前台 server：

```sh
./nb1 server --devices DEVICE_SERIAL
```

同一个程序既是 CLI，也是 server。它持有选定设备并监听本机回环地址，不会安装
登录服务。在另一个终端进入 CLI 目录，创建 `action.json`：

```json
{"action":{"kind":"pulse","color":"lime","cycles":2}}
```

```sh
./nb1 control devices
./nb1 control preview action.json
./nb1 control run DEVICE_SERIAL action.json --wait
```

在浏览器打开 `http://127.0.0.1:8931/demo/` 即可使用内嵌工作台。打开网页或预览
不会播放 Job。HTTP 接受不等于播放完成，应检查返回 Job 的终态。用 Ctrl-C 停止
前台 server。

如果只播放一次且不需要 HTTP，先让其他控制进程释放该设备，再显式选择 direct：

```sh
./nb1 control --direct run DEVICE_SERIAL action.json
```

direct 始终等待，使用相同的设备所有权锁。它拒绝 `--id` 和 `--endpoint`，Job 只在
当前调用中存在。显式设置 `--timeout` 可以限制播放等待，之后还有独立的清理等待。
`engine_closed` 只表示执行器已关闭，不等于播放成功或光学效果通过。
详见[直连生命周期边界](api-v1.md#cli-connection-policy)。

daemon 不可达时，普通 `control` 明确报错，由用户选择启动 daemon 或显式直连。
它不自动启动，也不静默切换到 USB。控制灯不需要键盘或输入监控权限。

## 可选的当前用户服务

macOS 使用 LaunchAgent，Linux 需要正在运行的 systemd user manager。Windows 的
托管 install/start/stop/uninstall 尚未实现。普通 Linux 容器可能没有 user manager，
此时可以使用前台 server。

如果明确选择在 macOS/Linux 安装服务，先停止占用相同设备或端口的前台 server，
再从稳定位置运行选定程序：

```sh
./nb1 service install --devices DEVICE_SERIAL
./nb1 service start
./nb1 service status --json
./nb1 control devices
```

install 启用登录加载，但只有附加 `--start` 才会请求立即启动。不传 `--binary` 时
使用当前正在运行的程序，不会从 PATH 搜索另一份。显式的绝对路径 `--binary`
可以固定另一份可信程序。服务已注册或 active，不等于 HTTP 或 USB 已就绪。

`service stop` 停止进程并保留登录配置。要移除托管服务，显式允许先停止：

```sh
./nb1 service uninstall --stop
```

升级与回滚都需要显式操作。手动解压的候选应保留旧目录，把核验过的新候选解压到
另一个目录，先用 `service uninstall --stop` 移除既有托管服务，再从选定候选运行
`service install --devices DEVICE_SERIAL --start`。沿用选定的 profile 与设备参数，
检查服务状态和 HTTP 设备状态。回滚时对保留的候选重复这些步骤。不要删除仍被服务
引用的目录。

Homebrew 候选另有稳定的 `opt/nb1/bin/nb1` 入口；明确升级或回滚安装包后，需要
显式 stop/start 才进入对应版本。没有自动重启、`service restart` 命令或
`brew services` 集成。调整服务的设备列表／监听地址也需要显式卸载、重装。
详见 [Homebrew 生命周期说明](../packaging/homebrew/README.zh-CN.md)。

本机颜色与 Pattern 仍保存在磁盘。内存 Job 与判重记录不跨进程重启保留，旧播放不会
自动恢复。本文不承诺任意候选之间的数据向后兼容。

## 独立接入 MCP 或 Skill

将同版 MCP 归档解压到另一个新目录，像 CLI 一样检查内部 `SHA256SUMS`。
配置 MCP 宿主启动 `nb1-mcp` 的绝对路径，并传入：

```text
--endpoint http://127.0.0.1:8931 --timeout 10s
```

它通过 stdio 实现 MCP 协议，再通过 HTTP 调用已有 daemon，不是启动 CLI 子进程，
不另占 USB，也不启动 daemon。stdout 只传协议消息，诊断走 stderr。适配器使用自己
的 `--endpoint`，不读取 nb1 YAML；如果换了端口，应向宿主提供实际地址。
详见 [MCP 工具与恢复规则](../adapters/mcp/README.md)。

安装 Skill 时，回到候选目录并解压到一个新的文件夹：

```sh
mkdir -m 700 blink1-control
unzip "blink1-control-<commit12>.zip" -d blink1-control
```

把得到的完整 `blink1-control` 文件夹复制到项目的 `.agents/skills/` 下，保留
`agents/` 和 `references/`。已有同名副本时，先核对再决定是否替换。Skill 是操作
说明，不自带程序、daemon、MCP 注册或额外权限。详见 [Skill 安装与使用](agent-guide.zh-CN.md)。
