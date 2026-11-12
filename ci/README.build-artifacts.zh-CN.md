# 同版候选：CLI、MCP、内嵌 Web 与 Skill

`build.yml` 保留 master push/PR 与手动入口，只有只读仓库权限；不打 tag、不创建 Release、不发布 tap 或镜像。五个原生目标为 Linux amd64/arm64、macOS arm64/amd64、Windows amd64。首次实体交付范围仍为已验证的 Mac arm64 / MK2 / FW204；有归档不代表该平台已完成 USB 或服务验收。

## 从源码到归档

1. 固定 Go1.27.3、GOWORK=off、只读模块；CLI 以 CGO=1 构建，核对干净源码 VCS、GOOS/GOARCH、SDK/GID 精确公开版本及 sum、内嵌 Web 锁。
2. 同一 checkout 的 `adapters/mcp` 以 CGO=0、`-buildvcs=true` 编译独立 `nb1-mcp`。仅允许 core 的本地 `../..` 替换，必须解析到同一源码根；不借此放宽 CLI 的 replacement 检查。
3. CLI 运行隔离配置的帮助检查。MCP 使用真实 stdio 完成初始化、12 工具发现，并验证无 daemon 的只读设备查询明确失败。它不打开 USB、不调用 CLI、不提交 Job。
4. `candidate-suite.py` 将原 CLI 五文件、独立 MCP 五文件、独立 Skill 五文件分别打包；Unix 二进制使用 tar.gz，Windows 使用 zip，Skill 一律 zip。`suite.json` 与外层 `SHA256SUMS` 绑定完整提交、目标、Web、模块、二进制及文件散列，全部标明 `release:false`。
5. 离线 `verify` 在临时目录检查准确文件集合、路径、重复项、链接/特殊文件、大小、可执行权限与散列；不执行包内程序。构建工作流另外运行实际 CLI/daemon 的无设备验收，保留原有分层证据。

原 CLI `check-build-artifact.py stage/verify/smoke` 保持独立；套件在其验证基础上增加独立 MCP 与 Skill。所有 stage 输出必须是新目录，失败时不上传成功候选。Actions artifact 中包含三份可解压归档与两个清单，不再依赖 Actions zip 保存原始二进制的执行权限。

## 本地使用

先在干净源码内构建并 stage CLI，然后构建同源 MCP；输出放仓外，避免污染 VCS 身份。打包命令：

```sh
python3 -B ci/candidate-suite.py stage \
  --root /absolute/path/to/clean-checkout \
  --cli-dir /absolute/path/to/staged-cli \
  --mcp-binary /absolute/path/to/nb1-mcp \
  --output /absolute/path/to/new-suite
python3 -B ci/candidate-suite.py verify --directory /absolute/path/to/new-suite
python3 -B ci/mcp-candidate-smoke.py \
  --binary /absolute/path/to/nb1-mcp --output /absolute/path/to/new-mcp-report.json
```

面向使用者的完整校验、解压、配置、前台 server、显式 direct、可选服务、升级回退与 MCP/Skill 安装见[英文指南](../docs/candidate-install.md)或[中文指南](../docs/candidate-install.zh-CN.md)。检查器是 matching checkout 的工具，不放进二进制归档。

SHA256SUMS 检查文件自洽，不是签名或独立来源证明。候选没有稳定公开下载地址、正式版本、远程 tap 或完整 OS 最低版本资格。源码编译通过、CLI help、MCP 协议、daemon 软件验收和实体灯光分别判断；既有 tests/race/vet、Go 下限、Homebrew 生命周期、服务与容器验收不被本流程替代。
