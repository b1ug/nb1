# nb1 MCP adapter

The adapter exposes the nb1 API v1 through the official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk), using JSON-RPC over stdio. It connects to a running local nb1 daemon over HTTP. The daemon selects devices, compiles Actions, executes Sequences, and stores local colors and Patterns.

The v0.1.3 platform archive includes MCP in the `nb1` executable. For local stdio, configure the client to launch the executable with `mcp` as its first argument. If upgrading from a v0.1.2 `nb1-mcp` configuration, keep any existing flags after that argument. [Client setup and migration](../../docs/install.md#connect-a-local-mcp-client)

An agent on another machine can connect directly to the daemon’s HTTPS `/mcp` endpoint with a Bearer token. Both transports expose the same twelve tools. [Remote setup](../../docs/remote-access.md)

## Build and run

Build nb1 in the repository root with Go 1.25.8 or newer.

```sh
GOWORK=off go build -o nb1 .
./nb1 mcp --endpoint http://127.0.0.1:5885 --timeout 10s
```

Stdout carries MCP protocol messages and stderr carries diagnostics. The endpoint defaults to `http://127.0.0.1:5885`. For a daemon on the v0.1.2 default port, pass `--endpoint http://127.0.0.1:8931`. When the daemon sets `server.local_token_file`, pass `--token-file PATH`.

The implementation uses the official `github.com/modelcontextprotocol/go-sdk v1.8.0`. Both stdio and HTTPS report the main executable’s version during initialization. Source consumers of the previous `nb1-mcp` name can build the compatibility launcher in this directory from a complete checkout. Historical downloads are available with the [v0.1.2 release](https://github.com/b1ug/nb1/releases/tag/v0.1.2).

## Tools

| Tool | Arguments | HTTP operation |
| --- | --- | --- |
| `list_devices` | `{}` | `GET /v1/devices` |
| `get_device_state` | `serial` | `GET /v1/devices/{serial}` |
| `resolve_color` | `color` | `POST /v1/colors/resolve` |
| `preview_light` | One of `action`, `sequence`, `pattern_id`; optional `serial` | `POST /v1/preview` |
| `run_light` | One of `action`, `sequence`, `pattern_id`, `trigger`; optional `id`, `serial` and `policy` | `POST /v1/jobs` |
| `get_job` | `id` | `GET /v1/jobs/{id}` |
| `cancel_job` | `id`, optional `serial` | `POST /v1/jobs/{id}/cancel`, with `?serial=…` for candidate device-guarded cancellation |
| `list_colors` | `{}` | `GET /v1/colors` |
| `save_color` | `name`, `color` | `PUT /v1/colors/{name}` |
| `list_patterns` | `{}` | `GET /v1/patterns` |
| `get_pattern` | `id` | `GET /v1/patterns/{id}` |
| `save_pattern` | `id`, one of `action`, `sequence`; optional `title` | `PUT /v1/patterns/{id}` |

Tool schemas describe the same Action kinds, Sequence timing rules, and color forms as the [API reference](../../docs/api-v1.md). Both Action colors and authored Sequence target colors accept Hex3/Hex6, names, RGB8/HSV objects and `@local-name`; normalized Sequences and saved Patterns contain resolved lowercase Hex6 values. Input validation rejects unknown fields, duplicate keys and explicit null values. Raw color numbers and source objects retain their original JSON meaning through HTTP. The daemon owns palette resolution and authoritative compilation.

Examples below show the arguments supplied to `tools/call`.

On a daemon supporting playback policies, `run_light` accepts `"policy":"queue"` to wait in order, or `"policy":"preempt"` to interrupt the current Job on the selected light and run before its existing queue. Omitting the field uses the default busy rejection. After an uncertain submission, query the original Job ID.

Preview is optional. `run_light` validates and submits the expression directly; it does not require a prior `preview_light` call. Use preview when you want to inspect the plan without playback.

With the routing candidate, `{"trigger":"build.done"}` resolves through the daemon’s local mapping. The Job returns its final serial and the optional `target_source`. Provide an explicit serial with the published v0.1.2 daemon. [Candidate routing examples](../../docs/trigger-routing.md).

```json
{"name":"preview_light","arguments":{"action":{"kind":"blink","color":"lime","cycles":2}}}
```

```json
{"name":"run_light","arguments":{"serial":"20000001","id":"agent-task-done","action":{"kind":"pulse","color":"lime"}}}
```

```json
{"name":"get_job","arguments":{"id":"agent-task-done"}}
```

```json
{"name":"save_color","arguments":{"name":"status-ok","color":{"space":"rgb8","r":0,"g":255,"b":0}}}
```

```json
{"name":"save_pattern","arguments":{"id":"task-done","title":"Task complete","action":{"kind":"pulse","color":"@status-ok"}}}
```

```json
{"name":"run_light","arguments":{"serial":"20000001","pattern_id":"task-done"}}
```

Pattern saving fixes the resolved Sequence. Pattern lists contain ID and title summaries; `get_pattern` returns the complete saved Sequence. Color names follow `[a-z][a-z0-9-]{0,31}`. Pattern and caller-supplied Job IDs accept 1–64 ASCII letters, digits, hyphens and underscores. Pattern titles accept up to 256 Unicode characters.

## Results and recovery

Each tool returns matching JSON in `structuredContent` and its text fallback. The result contains `data` with the corresponding HTTP response and the `endpoint` to use for recovery. Stdio returns the selected loopback HTTP origin. Streamable HTTP returns the daemon’s external HTTPS `/mcp` URL. Job operations also retain `id`; `save_color` retains `name`.

```json
{
  "endpoint":"http://127.0.0.1:5885",
  "id":"agent-task-done",
  "data":{"id":"agent-task-done","serial":"20000001","status":"running","completed_steps":0,"total_steps":4,"created_at":"2026-11-11T00:00:00Z"}
}
```

`run_light` sends one submission. An omitted or empty `id` generates `j_` followed by ten random Crockford Base32 characters before that submission. Callers can supply their own ID before running a task. A lost or malformed acceptance response returns `isError:true`, the original ID and endpoint, and `error.outcome_unknown:true`. Use `get_job` for that same ID on that same endpoint. The adapter performs no submission retry.

A definite API rejection preserves its error code and HTTP status. HTTP timeouts and MCP session closure leave accepted daemon Jobs under daemon control. Cancellation is a separate `cancel_job` operation; inspect `get_job` while status is `cancelling`.

For guarded cancellation, send the accepted Job's `id` and `serial` to the same
daemon. A different device returns `409 job_target_mismatch` without modifying
the Job. Older daemons reject the query, and the adapter preserves that response
without retrying the request.

Successful tool transport does not establish a successful lighting task. Inspect `data.status` for `queued`, `running`, `cancelling`, `succeeded`, `cancelled` or `failed`. Job deduplication applies during active execution and the retained terminal record's lifetime. Terminal records default to a thirty-minute expiration and 16,384-entry LRU retention. The daemon can configure these startup limits through `jobs.terminal_ttl` and `jobs.terminal_limit`; capacity eviction can occur before the TTL, and daemon restart clears memory. An unavailable record leaves the earlier execution outcome unresolved.

Local color and Pattern saves also send one PUT. A transport failure can leave their save outcome unknown. Inspect `list_colors` or `get_pattern` before deciding whether to save again.

## Validation

```sh
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```

Tests launch real stdio subprocesses using the official SDK's `CommandTransport`. They exercise the canonical HTTP handlers, a real core Engine with a synthetic backend and local library, busy rejection, active cancellation, strict arguments, preserved color-number lexemes, lost and malformed acceptance responses, timeout recovery, and same-ID lookup. They verify clean subprocess exit on stdio EOF. These tests provide software evidence with synthetic devices.

See the [core terminology](../../docs/api-v1-terminology.md) and [MIT license](../../LICENSE).
