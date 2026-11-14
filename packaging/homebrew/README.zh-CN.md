# Homebrew 配方与 Linux 容器验证

内部中文说明，2026-11-12。对外说明以 [English README](README.md) 为准。

## 当前交付状态

源码 Formula 模板已覆盖 macOS 与 Linux；最低 macOS 要求只在 macOS 生效。当前没有已发布的新 Release 或远程 tap。正式渲染器仍要求真实 tag 归档、SHA-256、commit、最低 macOS 与 nb1-only 组件范围，缺项继续拒绝，不用测试值伪装正式发布。

构建要求稳定版 Go 1.27.4 或更新版本。配方在模块下载前和编译前分别读取实际的 `go env GOVERSION`，并使用 `GOTOOLCHAIN=local`；仅检测到已经安装 Go 依赖还不够。旧版本、预发布版、开发版或无法识别的版本会在这些操作前明确拒绝。用户更新 Homebrew 的 Go 依赖后再重试，配方不会自动下载另一套工具链。

本次按用户要求开展真实 Homebrew 安装验证：从准确 Git 提交生成源码归档并计算校验和，在容器里建立本地测试 tap。候选包有明确的 dev 版本与来源，不替代正式发布输入，也不创建 tag。

## 普通用户如何使用

安装包只安装程序，不连接灯、不注册或启动服务。用户可执行 `nb1 list`，或用 `nb1 server --devices SERIAL` 前台常驻；需要登录服务时执行 `nb1 service install --devices SERIAL`，再显式 `service start`，或安装时指定 `--start`。省略 `--binary` 时，Homebrew 构建使用已校验的稳定包入口；普通源码构建使用当前可执行文件。显式 `--binary` 固定指定文件。

macOS 使用当前用户 LaunchAgent；Linux 需要已运行的 systemd 用户 manager。当前配方没有 Homebrew 的 service 块，因此服务管理入口是 `nb1 service`，尚不提供 `brew services start nb1`。

## Linux 容器验什么

采用干净 Ubuntu 24.04 / arm64、固定 Homebrew 提交及真实 CGO 构建。普通 UID、无额外 capabilities、无宿主挂载/USB 映射；升级验收期间保留网络用于获取包和模块，不映射宿主端口。

- 实际 `brew install`、`brew test`、动态链接检查与卸载。
- 安装后的 `list` 与 `list --firmware` 应成功返回没有设备。
- Linux unit 渲染与只读配置状态。
- 无用户 manager 时，service install/status/start 应有界失败、保留 unknown，不创建 unit 或登录启用链接；不能因此称为服务启停成功。
- 实际 nb1 daemon 的 HTTP 健康、配置设备离线、纯预览、离线 Job 拒绝、缺失 Job 查询与 SIGTERM 正常退出。

模板自身的 test 只做帮助与 unit 渲染，不访问 USB 或安装服务；独立容器验收补充运行时行为。真实 systemd manager、登录/重启和 Linux USB 需要各自的证据。

## 升级路径与联合验收

Homebrew 构建在链接时绑定 `opt/nb1/bin/nb1` 和 canonical `Cellar/nb1`，安装及显式启动复核所有权、符号链接经过的目录与写权限。包入口只能落在对应 rack 中。配方把二进制权限统一为0755，避免 umask002 生成组可写程序。Linux组可写目录只允许位于当前用户私有目录边界内；共享可写路径不会静默信任。

升级包不会替换已经运行的进程。用户显式 `service stop` / `service start` 后进入新版本；删除旧 keg 后不必重写服务文件。回退包版本也需要显式停止、启动。Job及去重缓存位于内存，重启后不恢复或重播；本机色库和Pattern是不同的磁盘数据。

`check_upgrade_service.py` 用真实Git归档和本地候选tap依次验证安装、升级、删除旧keg、重启、回滚与清理。available模式要求普通用户和可信独立CLI，拒绝覆盖现有包及用户服务；核对实际进程可执行文件及未被重写的服务定义。unavailable模式只验普通Linux容器的受控失败与前台daemon，不能替代真实systemd验收。专门Actions工作流另跑真实用户manager。

正式发版仍需选择真实tag/归档与包布局、建立远程tap；MCP是独立module，当前配方不捆绑。Windows分发和服务管理、Linux USB与登录/重启均有独立边界。源码说明不代替平台验收收据。

## 本地离线检查

```sh
python3 -B -m unittest discover -s packaging/homebrew -p 'test_*.py' -v
python3 packaging/homebrew/render_formula.py --input packaging/homebrew/release-input.required.json --check-only
```

第二条在正式发布输入未补齐时应非零退出。可设置 `NB1_HOMEBREW_TEST_BINARY=/absolute/path/nb1`，让现有 Ruby 适配检查实际候选的帮助与纯 service render；它本身仍不是 brew 安装验收。
