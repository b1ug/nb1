# Homebrew 配方与 Linux 容器验证

内部中文说明，2026-11-11。对外说明以 [English README](README.md) 为准。

## 当前交付状态

源码 Formula 模板已覆盖 macOS 与 Linux；最低 macOS 要求只在 macOS 生效。当前没有已发布的新 Release 或远程 tap。正式渲染器仍要求真实 tag 归档、SHA-256、commit、最低 macOS 与 nb1-only 组件范围，缺项继续拒绝，不用测试值伪装正式发布。

本次按用户要求开展真实 Homebrew 安装验证：从准确 Git 提交生成源码归档并计算校验和，在容器里建立本地测试 tap。候选包有明确的 dev 版本与来源，不替代正式发布输入，也不创建 tag。

## 普通用户如何使用

安装包只安装程序，不连接灯、不注册或启动服务。用户可执行 `nb1 list`，或用 `nb1 server --devices SERIAL` 前台常驻；需要登录服务时执行 `nb1 service install --devices SERIAL`，再显式 `service start`，或安装时指定 `--start`。省略 `--binary` 会使用当前可执行文件。

macOS 使用当前用户 LaunchAgent；Linux 需要已运行的 systemd 用户 manager。当前配方没有 Homebrew 的 service 块，因此服务管理入口是 `nb1 service`，尚不提供 `brew services start nb1`。

## Linux 容器验什么

采用干净 Ubuntu 24.04 / arm64、固定 Homebrew 提交及真实 CGO 构建。普通 UID、无额外 capabilities、无宿主挂载/USB 映射；构建后断开外网，再执行运行时检查。

- 实际 `brew install`、`brew test`、动态链接检查与卸载。
- 安装后的 `list` 与 `list --firmware` 应成功返回没有设备。
- Linux unit 渲染与只读配置状态。
- 无用户 manager 时，service install/status/start 应有界失败、保留 unknown，不创建 unit 或登录启用链接；不能因此称为服务启停成功。
- 实际 nb1 daemon 的 HTTP 健康、配置设备离线、纯预览、离线 Job 拒绝、缺失 Job 查询与 SIGTERM 正常退出。

模板自身的 test 只做帮助与 unit 渲染，不访问 USB 或安装服务；独立容器验收补充运行时行为。真实 systemd manager、登录/重启和 Linux USB 需要各自的证据。

## 当前缺口

服务安装会解析符号链接并固定真实二进制路径，Homebrew 下可能固定到某个 Cellar 版本。稳定服务路径或显式重新绑定仍待实现和验收；不能由本次安装成功推断升级清理旧版本后服务仍有效。

正式发版仍需选定 tag/归档与包布局，建立远程 tap，并做原生 Mac 安装/升级及真实 Linux 用户服务验证。MCP 另有 module，当前配方不捆绑；Windows 走独立分发路径。

## 本地离线检查

```sh
python3 -B -m unittest discover -s packaging/homebrew -p 'test_*.py' -v
python3 packaging/homebrew/render_formula.py --input packaging/homebrew/release-input.required.json --check-only
```

第二条在正式发布输入未补齐时应非零退出。可设置 `NB1_HOMEBREW_TEST_BINARY=/absolute/path/nb1`，让现有 Ruby 适配检查实际候选的帮助与纯 service render；它本身仍不是 brew 安装验收。
