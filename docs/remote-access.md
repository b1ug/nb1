# Control a blink(1) from another computer

[简体中文](remote-access.zh-CN.md)

Plug the lamp into the computer running `nb1 server`. An MCP client on a
second computer can connect to that daemon over HTTPS, select a device by
serial, and play a saved Pattern or an Action. Both computers see the same
Jobs and the library on the device host. Use a build that includes remote
MCP. The published v0.1.2 daemon supports the local stdio adapter.

## On the computer with the lamp

1. Run `nb1 list` and note the serial. Choose a LAN address on this computer
   and a client-visible DNS name or IP. Provide a TLS certificate and key for
   that name or IP. The client must trust its issuing CA.
2. In the directory where you will keep the configuration, run
   `nb1 config token init access.token`. This generates a private file once
   and prints its path. You can instead supply your own private file containing
   one Bearer token of at least 32 characters. Letters, digits and `-._~+/` are
   allowed, with optional trailing `=` padding. The daemon user must own the file. Transfer the token to the client through your usual secret store.
3. Save this configuration as `nb1.yaml`. Replace the example serial, IP,
   hostname and file paths with yours.

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

`listen` binds a specific interface. `host` is the authority clients use in
their URL, including the port for HTTPS ports other than 443. Omit `host` to
use the bound IP and port. Credential paths resolve beside the selected YAML
file. The HTTPS port is your choice. The local API and debugger still share
`http://127.0.0.1:5885` by default.

## On the computer running the agent

Choose **Streamable HTTP**, enter `https://nb1.example.test:9443/mcp`, and
configure `Authorization: Bearer <token>` on each request. The client needs
network access to the configured port. The certificate must match the name
in this URL. Keep the token in the client's credential store or environment.

For example, a Codex CLI client can read the token from an environment
variable. In the shell that will launch Codex, load the private client copy
and register the endpoint once.

```sh
export NB1_MCP_TOKEN="$(cat ./access.token)"
codex mcp add nb1-lan --url https://nb1.example.test:9443/mcp \
  --bearer-token-env-var NB1_MCP_TOKEN
```

The next time you launch the client, make the variable available again.
The server URL and the variable name are the only settings needed here.

| MCP tool | Arguments | What to check |
| --- | --- | --- |
| `list_devices` | `{}` | Find the intended serial and confirm its state is `online`. |
| `preview_light` | `{"serial":"20000001","action":{"kind":"pulse","color":"teal","cycles":2}}` | Inspect the plan on screen. |
| `run_light` | `{"serial":"20000001","id":"desk-check-01","action":{"kind":"pulse","color":"teal","cycles":2}}` | Plays the lamp. Use the actual serial and choose a fresh ID for a new play. |
| `get_job` | `{"id":"desk-check-01"}` | Read the accepted Job until it reaches a terminal state. |

Preview is optional. A lost connection can leave an accepted Job running.
Reconnect to the same HTTPS `/mcp` URL and query the original Job ID.
The remote tool result includes that URL in `endpoint`. [All twelve tools](../adapters/mcp/README.md#tools)

The same listener also serves the device HTTP API at `/v1`. For example,
`GET /v1/devices` lists the devices for a REST client. Send the same Bearer
header on reads and writes. Anyone with the token can operate the configured
lights and their shared library. [HTTP API](api-v1.md)

## Local-only operation and local authentication

Remote access is off by default. Set `server.remote.enabled: false` and restart
the daemon to return to local-only operation. Its remote certificate and
token paths can stay in the file. If a proxy or tunnel forwards traffic to
loopback, clients of that forward can still reach the local listener.

The local listener accepts requests without a token by default. To require one,
generate a separate file with `nb1 config token init local.token` and set
`server.local_token_file: local.token`. Restart the daemon after changing
either token, TLS credentials or listener settings.

- `nb1 control` and `nb1 doctor` read the token file named in the active
  profile when using its endpoint. When choosing an endpoint explicitly, pass both
  `--endpoint http://127.0.0.1:5885` and `--token-file ./local.token`.
- Run `nb1 mcp` from the same platform download to serve local stdio. Pass
  `--token-file ./local.token` when the local API requires one.
- With a local token, `GET /healthz` and the constant `GET /v1/presence`
  marker remain readable. `GET /v1/service-status` requires the token.
  The WebHID page can confirm that nb1 answered, with device counts shown as
  unknown. The embedded debugger is unavailable in this mode.

## Connection problems

| What the client sees | What to check |
| --- | --- |
| Connection refused or timed out | Check the daemon startup output, `remote.enabled`, the LAN interface, DNS and firewall access to the chosen port. |
| TLS certificate verification failed | Use the DNS name or IP covered by the certificate. Install its issuing CA on the client. |
| `401 unauthorized` | Set exactly one Bearer header using the remote listener's token. Reload client credentials after rotation. |
| `403 invalid_host` or `invalid_origin` | Match the URL to `remote.host`, including its port. A client that sends `Origin` must send this HTTPS origin. |
| `404` at `/mcp` or `405` for a browser GET | Use the HTTPS listener and select Streamable HTTP in the MCP client. The endpoint handles stateless JSON-RPC POST requests. Opening the URL in a browser does not initialize a session. |
| MCP connects but playback fails | Call `list_devices` again. Check the selected serial and the Job result on this daemon. A busy or offline device retains its own target. |
