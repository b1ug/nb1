# 安装与使用 nb1 v0.1.1

[English](install.md)

从 [nb1 v0.1.1](https://github.com/b1ug/nb1/releases/tag/v0.1.1) 下载匹配的程序。CLI、本地 server 和完整 Nightlab 调试器都在同一个 `nb1` 可执行文件里。使用预编译程序不需要安装 Go、Node.js 或单独的 Web，也不需要浏览器 WebHID 授权。

## 下载与核验

| 电脑 | CLI 包 |
| --- | --- |
| Apple Silicon Mac | `nb1-v0.1.1-darwin-arm64.tar.gz` |
| Intel Mac | `nb1-v0.1.1-darwin-amd64.tar.gz` |
| Linux arm64 | `nb1-v0.1.1-linux-arm64.tar.gz` |
| Linux amd64 | `nb1-v0.1.1-linux-amd64.tar.gz` |
| Windows amd64 | `nb1-v0.1.1-windows-amd64.zip` |

本次预编译 CLI／server 在两种 Mac 架构上的二进制最低版本均为 macOS 15.0；Linux CLI 需要 glibc 2.38 或更新版本。可选 MCP 程序的 macOS 二进制最低版本为 13.0，Linux MCP 为静态链接；它仍需要兼容的 nb1 server，因此不会降低本机 server 的要求。macOS 下载包未经过 Developer ID 公证。

这些二进制要求与源码兼容性分开，不保证所有操作系统或实体 USB 组合。使用较旧系统前，请查看对应 Release 的构建和平台记录。已有 macOS arm64／MK2／FW204 的有限实物证据不扩大为所有平台、MK3、重启或持久化保证。Windows 支持前台运行，不包含自动服务管理。

从同一 Release 下载所选包、`release.json` 和全局 `SHA256SUMS`，核对完整 SHA-256 与准确文件名。Apple Silicon Mac 示例：

```sh
shasum -a 256 nb1-v0.1.1-darwin-arm64.tar.gz
```

Linux 可用 `sha256sum`，Windows PowerShell 可用 `Get-FileHash -Algorithm SHA256`。全局清单还列着其他平台的包，仅下载一个平台时不要把其他文件缺失误判成当前包损坏。校验和检查内容一致性，不是独立签名；下载包和预期清单都应来自可信的 Release 页面。

解压到独立的新目录，再检查包内清单。macOS 示例：

```sh
mkdir nb1-v0.1.1
tar -xzf nb1-v0.1.1-darwin-arm64.tar.gz -C nb1-v0.1.1
cd nb1-v0.1.1
shasum -a 256 -c SHA256SUMS
./nb1 version --json
```

Linux tar 包用 `tar -xzf` 解压及 `sha256sum -c` 检查。Windows 解压 ZIP 后运行 `nb1.exe`。`build.json`、Release 清单和 `version --json` 应指向同一来源；它们描述下载的程序，不代表另一份已运行 daemon 的版本。若要注册服务，请把目录放在稳定位置。

## 用 Homebrew 安装

[公开 b1ug tap](https://github.com/b1ug/homebrew-tap) 安装该 tap 当前实际发布的版本：

```sh
brew tap b1ug/tap
brew install b1ug/tap/nb1
nb1 version
```

Homebrew 从已发布源码编译，并安装所需构建依赖。Formula 要求 macOS 15 或受支持的 Linux Homebrew 环境，编译需要 Go 1.27.4 或更新版本。上文预编译 Linux 程序的 glibc 下限不能直接套用到 Homebrew 源码构建。安装前查看 tap 当前版本及平台记录。使用 Homebrew 时，后续命令中的 `./nb1` 改为 `nb1`；`./nb1` 指向手动解压的程序。

[双语文档站](https://b1ug.github.io/nb1-docs/zh/)提供安装与使用说明。安装 nb1 不会自动注册或启动后台服务。

## 打开内嵌调试器

```sh
./nb1 list
./nb1 server --devices DEVICE_SERIAL
```

把 `DEVICE_SERIAL` 换成真实灯的序列号，再打开 **http://127.0.0.1:8931/demo/**。选择设备、灯效或 Sequence，直接点 **Play**；Preview 可选，Play 会校验当前草稿。打开页面、编辑颜色或屏幕预览不会向真实灯提交任务。

页面、字体、编辑器、3D 模型和双面 fallback 都已内嵌，无需外置 Web 目录或 CDN。默认英文，可切中文。若改过 `server.listen` 或 `--listen`，请使用实际回环监听地址加 `/demo/`。

浏览器、CLI 和 MCP 共用这个 daemon。HTTP 可达、设备在线、任务已接受、任务已完成分别是不同状态；操作失败或结果不确定时，按页面就近提示查询原 Job，不要换 ID 重放。Ctrl-C 请求前台 server 正常退出；清理未确认时，不为同一只灯启动第二个拥有者。独立 WebHID 网页另外占用 USB，交接前先 Disconnect。

## CLI 与显式直连

把内容存为 `action.json`：

```json
{"action":{"kind":"pulse","color":"lime","cycles":3}}
```

在另一个终端运行：

```sh
./nb1 control run DEVICE_SERIAL action.json --wait
```

需要只看计划、不播放时，单独运行 `./nb1 control preview action.json`。结果 JSON 写 stdout，诊断写 stderr；丢失接受回执后，应保留原 Job ID 与地址查询，而不是重新提交。字段、默认值、错误及恢复规则见 [API 参考](api-v1.md)。

无需 daemon 的一次前台播放，应先让原控制器释放设备，再显式直连：

```sh
./nb1 control --direct run DEVICE_SERIAL action.json
```

直连始终等待，Job 只属于本次进程。HTTP 不可达不会自动切 USB。成功的 hold 可以保留灯色；需要熄灭时明确执行 Off。

CLI 的帮助与程序诊断在英文、中文和其他 locale 下均保留英文；Web 工作台有独立语言切换。终端颜色按实际输出流判断：非空 `NO_COLOR` 优先禁色，`CLICOLOR=0` 禁用普通着色；没有设置 `NO_COLOR` 时，可用 `CLICOLOR_FORCE=1` 显式强制颜色。管道或 `TERM=dumb` 默认使用纯文本。JSON 始终不着色。

## 可选用户服务

下载／解压不会安装或启动服务。macOS 或有用户级 systemd manager 的 Linux 上，先停掉前台 server，再从选定的稳定目录显式安装：

```sh
./nb1 service install --devices DEVICE_SERIAL
./nb1 service start
./nb1 service status --json
```

install 安排以后登录时可能加载；未加 `--start` 不请求本次立即启动。默认使用当前程序，可用 `--binary` 显式选择另一份可信绝对路径。注册／active 不等于 HTTP 或 USB 已就绪。

`service stop` 保留登录配置；`service uninstall --stop` 明确允许停止并删除托管配置。Windows 没有自动安装／启停／卸载服务；没有用户 manager 的 Linux 容器使用前台 `nb1 server`。

手动解压升级时，保留旧目录、核验并解压新版本，然后显式卸载旧托管服务并从新程序重新安装，使用相同 profile 和设备设置。不要删除服务仍引用的目录。Homebrew 用 `brew upgrade b1ug/tap/nb1` 升级，再在合适时显式停止／启动托管 daemon。默认服务绑定跟随稳定的 `opt/nb1/bin/nb1` 入口；显式 `--binary` 仍指向固定路径。操作见 [tap 生命周期说明](https://github.com/b1ug/homebrew-tap#upgrade-and-remove)。服务由 `nb1 service` 管理，不使用 `brew services`。

## 配置、MCP 与 Skill

`./nb1 config path/init/show` 可以不访问 USB 地查看位置、创建模板与检查解析结果。Mac/Linux 默认 profile 为 `~/.config/nb1/config.yaml`，设置绝对 `XDG_CONFIG_HOME` 时跟随该根目录；Windows 使用原生应用配置目录。本机颜色和 Pattern 存在电脑上，不是设备 RAM／Flash。

可选同版下载：

- `nb1-mcp-v0.1.1-<os>-<arch>.tar.gz`（Windows 为 `.zip`）：解压到另一新目录，核验包内清单，再配置 MCP host 使用绝对 `nb1-mcp` 路径及参数 `--endpoint http://127.0.0.1:8931 --timeout 10s`。初始化报告 `v0.1.1`；真实 stdio 协议连接本机 HTTP，不包装 CLI，也不占 USB。
- `blink1-control-v0.1.1.zip`：把内容解压到新的 `blink1-control` 文件夹，再完整复制到项目的 `.agents/skills/`，保留 `SKILL.md`、`agents/` 与 `references/`。Skill 不安装程序、注册 MCP、启用全局 hook 或扩大权限，见 [Agent 指引](agent-guide.zh-CN.md)。

MCP 单独选择 endpoint，不读取 nb1 YAML；各客户端应指向同一份目标 daemon。API 校验 Host 与浏览器 Origin，但 loopback 不是用户身份认证，同机能调用端口的其他进程仍可能控制已配置的灯。本地调试器不需要键盘、输入监控或相机权限。

这个版本包含当前 Action／Sequence、Job 与本机库能力；Trigger、新可编辑内容目录、queue／replace 和设备原生部署 API 仍为独立计划。[开发候选安装指南](candidate-install.zh-CN.md)保留 `release:false` 的 CI 套件约定，不能把它和本正式版包混用。
