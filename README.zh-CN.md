# nb1

控制 blink(1) 指示灯的命令行工具，也可作为本机 daemon 提供共享的版本化HTTP API（兼容v1与实验v2）。

将示例中的 `20005601` 换成实际设备序列号。先在一个终端启动 daemon：

```sh
nb1 server --devices 20005601
```

在另一个终端通过共享入口操作或诊断：

```sh
nb1 control devices
nb1 control set 20005601 '#3366ff' --wait
nb1 doctor
```

`control` 默认连接 `http://127.0.0.1:8931`，可用 `--endpoint` 指定本机端口；临时直控使用 `nb1 control --mode direct ...`。`doctor` 只读检查版本、daemon 和已配置设备快照，不扫描或打开 USB。

`on`、`off`、`blink`、`fade`、`play`、`read`、`write` 等旧命令仍直接访问设备。例如 `nb1 --device 20005601 on` 不经过 daemon。已有 daemon 持有设备时，用 `control` 的默认 daemon 模式共享访问。

日常以普通用户运行，设备访问按系统权限配置；server 仅监听本机回环地址。更多参数与入口见 `nb1 --help`、`nb1 server --help`、`nb1 control --help` 和 `nb1 doctor --help`。

用户启动配置与macOS/Linux托管服务候选的 `install/start/stop/status/uninstall`，以及Windows候选只读 `service status`（管理与可靠stop未实现，普通用户原生查询待独立验收）用法见[用户级服务说明](internal/service/README.zh-CN.md)。

## 中文 API 参考

[完整手册](docs/api-reference.zh-CN.md)说明High/Low分别负责什么、当前完成状态、参数与任务语义；[OpenAPI](docs/openapi.json)、[实际MCP schema](docs/mcp-tools.schema.json)和[JSON示例](docs/examples/api/)供机器与调用方核对。开发接口与本机已安装版本分别标记，阅读不操作设备。

96a整合候选已提供High八动作/实验Orbit、Low Program/Frame、独立高级Pattern库、CLI七命令、真实stdio MCP16及动森风格本地图案书。Web来源f90ef8，9公开资源+LICENSE及manifest共208132B；`/demo/patterns.html`只在有效扩展bundle中存在。该版本有本机MK2有限CLI/MCP/Web验证，验收窗口曾恢复1d89后，另完成工作区安装96a与33项只读复核；不等于发布、全局MCP注册、OS服务安装或跨平台实体USB通过，详见手册的版本与证据段。
