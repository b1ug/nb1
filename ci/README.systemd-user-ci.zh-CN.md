# Linux systemd 用户服务 CI 接线

`.github/workflows/runtime.yml` 新增独立的 `systemd-user-lifecycle` job，显示名为 **Linux systemd user lifecycle / USB-free fixture**。它与既有9个软件/原生构建job分开，在 `ubuntu-24.04` 完整GitHub托管VM中运行，不使用容器或本机服务。当前提交只完成workflow静态验证；须以真实Actions结果及artifact确认原生PASS，不能先当作Linux实测通过。

job使用现有 `actions/checkout@v7`、`actions/setup-go@v7` 和Go 1.27.x，保持 `contents: read`、`persist-credentials: false`，不声明secrets。独立模块构建关闭GOWORK并核对与既有runtime-native基线一致的SDK/GID pin：

- blink1-go：`v0.0.2-0.20261109021311-c87c7c4888a0`，源码commit `c87c7c4888a037fcd08572e3dbe1272568e01d39`。
- GID：`v0.0.3-0.20261109020519-8915ba6f4beb`，源码commit `8915ba6f4beb8c6411b2e4d8051f7a75df898ac5`。

CLI与独立stdlib fixture都以 `CGO_ENABLED=0` 构建。被安装运行的是fixture，产品CLI只管理服务；这验证真实systemd用户manager、普通UID、NoNewPrivileges与生命周期，**不验证Linux USB或运行真实nb1 server**。验收工具范围见[工具说明](README.systemd-user.zh-CN.md)。

workflow显式传入 `--execute --binary ABS --fixture ABS --output NEWDIR`，并选择全部6项negative-case：external-enable、dropin、mask、transient、need-reload、bus-loss。sudo只由工具用于专用新账户/fixture的设置与清理；产品命令、文件观察和用户manager操作通过该普通UID与 `env -i` 执行，不继承runner token。

harness非零会直接使job失败，未使用continue-on-error、忽略错误或环境green skip。额外的always验证步骤要求：报告为PASS、明确USB-free、真实Linux/systemd环境成立、所有报告中的negative-case均PASS、非空清理记录全部PASS且没有cleanup_failed。缺失证据、PREPARED_NOT_RUN、ENVIRONMENT_UNAVAILABLE、负例未运行、清理失败都会红；后续新增负例而workflow遗漏选择时也不能静默变绿。超时/强制取消不能计为显式清理通过。

`actions/upload-artifact@v7` 的always步骤上传精选完整证据，保留14天：构建版本/pin元数据、构建日志、harness原始精选results.json及stdout JSON、harness stderr日志、独立verdict.json。artifact名包含 `systemd-user-usb-free-ubuntu-24.04`、run ID与attempt。不会上传新账户HOME、二进制、环境转储、token、journal或 `/proc/environ`；缺少所有可上传文件本身也会报错。官方输入及缺文件策略见[upload-artifact](https://github.com/actions/upload-artifact)。

本地审阅只需workflow/YAML、shell和嵌入Python静态验证；不要在开发机器运行此job的 `--execute`、sudo或服务操作。Linux用户服务原生验收与Linux USB、登录/重启自启、其他发行版的结论保持分开。
