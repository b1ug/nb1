# 从另一台电脑控制 blink(1)

[English](remote-access.md)

把灯插在运行 `nb1 server` 的电脑上。另一台电脑的 MCP 客户端可通过 HTTPS 连到这个 daemon，按序列号选灯并播放 Action 或已保存的 Pattern。两端看到的是同一台主机上的 Job 和内容库。设备端需要使用包含远程 MCP 的构建。已发布的 v0.1.2 支持本地 stdio 适配器。

## 在插灯的电脑上

1. 运行 `nb1 list` 取得序列号。选定这台电脑的局域网 IP 和客户端可访问的主机名或 IP，准备与之匹配的 TLS 证书和私钥。客户端需要信任签发证书的 CA。
2. 在准备保存配置的目录运行 `nb1 config token init access.token`。命令会创建私有文件并输出路径。也可以自行提供属于 daemon 用户的私有文件，其中仅写入一个至少 32 个字符的 Bearer Token。可使用英文字母、数字和 `-._~+/`，末尾可以有 `=` 填充。通过平时使用的凭据管理方式把 Token 交给客户端。
3. 将下面的配置保存为 `nb1.yaml`，替换示例中的设备序列号、地址、主机名和文件路径。

```yaml
server:
  devices: ['20000001']
  remote:
    enabled: true
    listen: '192.168.1.42:9443'
    host: 'nb1.example.test:9443'
    token_file: access.token
    tls_cert_file: server.crt
    tls_key_file: server.key
```

```sh
nb1 --config ./nb1.yaml server
```

`listen` 绑定具体网卡地址。`host` 对应客户端 URL 中的主机名或 IP，HTTPS 端口不是 443 时还要带端口。省略 `host` 时使用绑定的 IP 和端口。凭据和证书的相对路径以所选 YAML 所在目录为准。远程 HTTPS 端口可自行选定。本机 API 和内嵌调试器仍默认共用 `http://127.0.0.1:5885`。

## 在运行 Agent 的电脑上

选择 **Streamable HTTP**，填入 `https://nb1.example.test:9443/mcp`，配置客户端在每次请求中发送 `Authorization: Bearer <token>`。客户端应能访问配置的端口，URL 主机名应与证书匹配。Token 可以放在客户端的凭据存储或环境变量中。

例如，Codex CLI 可从环境变量读取 Token。在将要启动 Codex 的终端里加载私有 Token 文件，并注册一次服务地址。

```sh
export NB1_MCP_TOKEN="$(cat ./access.token)"
codex mcp add nb1-lan --url https://nb1.example.test:9443/mcp \
  --bearer-token-env-var NB1_MCP_TOKEN
```

下次启动客户端时，继续提供同名环境变量。客户端配置中只需保存服务地址和变量名。

| MCP 工具 | 参数 | 可见结果 |
| --- | --- | --- |
| `list_devices` | `{}` | 找到目标序列号，确认设备状态为 `online`。 |
| `preview_light` | `{"serial":"20000001","action":{"kind":"pulse","color":"teal","cycles":2}}` | 在屏幕上查看计划。 |
| `run_light` | `{"serial":"20000001","id":"desk-check-01","action":{"kind":"pulse","color":"teal","cycles":2}}` | 实灯播放。替换为实际序列号，每次发起新的播放时选用新 ID。 |
| `get_job` | `{"id":"desk-check-01"}` | 查询已受理的 Job，直到出现终态。 |

可以直接播放，预览可选。连接中断后，已受理的 Job 可能仍在运行。重新连接同一个 HTTPS `/mcp` 地址，用原 Job ID 查询。远程工具结果的 `endpoint` 字段也会返回这个地址。[全部十二个工具](../adapters/mcp/README.md#tools)

这个 HTTPS 监听器也在 `/v1` 提供设备 API，例如 REST 客户端可用 `GET /v1/devices` 列出设备。读取和写入均使用同一 Bearer 请求头。持有 Token 的客户端可控制已配置的灯并访问共享内容库。[HTTP API](api-v1.md)

## 只接受本机请求及本机鉴权

远程访问默认关闭。将 `server.remote.enabled` 设为 `false` 并重启 daemon，即可恢复只监听回环地址，配置中可保留远程凭据和证书路径。另行设置的代理或隧道仍可把流量转发到回环监听器。

本机 API 默认免 Token。如要开启本机鉴权，可用 `nb1 config token init local.token` 单独生成本机凭据，并在 `server` 下设置 `local_token_file: local.token`。更换任一 Token、证书或监听设置后，均须重启 daemon。

- `nb1 control` 和 `nb1 doctor` 使用当前配置中的地址时，会同时读取其中指定的本机 Token 文件。显式选择端口时，同时提供 `--endpoint http://127.0.0.1:5885` 和 `--token-file ./local.token`。
- `nb1 mcp` 和兼容的 `nb1-mcp` 均可提供本机 stdio。若本机 API 启用了鉴权，启动时传入 `--token-file ./local.token`。
- 启用本机鉴权后，`GET /healthz` 和 `GET /v1/presence` 固定标记仍可读取，`GET /v1/service-status` 需要 Token。WebHID 页面可确认服务已应答，设备数量显示未知。此模式下内嵌调试器暂停提供。

## 连接排错

| 客户端现象 | 检查方法 |
| --- | --- |
| 拒绝连接或超时 | 查看 daemon 的启动结果，核对 `remote.enabled`、网卡 IP、DNS 和防火墙放行的端口。 |
| TLS 证书校验失败 | 使用证书覆盖的主机名或 IP，在客户端信任签发证书的 CA。 |
| `401 unauthorized` | 为请求设置一个 Bearer 请求头，使用远程监听器的 Token。轮换后重新加载客户端凭据。 |
| `403 invalid_host` 或 `invalid_origin` | URL 要与 `remote.host` 完全对应，包括端口。若客户端发送 `Origin`，其值须与这个 HTTPS 来源一致。 |
| `/mcp` 返回 `404`，或浏览器打开时返回 `405` | 核对 HTTPS 监听地址，在 MCP 客户端选择 Streamable HTTP。该端点用无状态 JSON-RPC POST 处理请求，浏览器地址栏发出的 GET 不能初始化会话。 |
| MCP 已连接，播放失败 | 重新调用 `list_devices`，核对这台 daemon 中的设备序列号和 Job 结果。设备忙碌或离线时，请求仍指向原来的灯。 |
