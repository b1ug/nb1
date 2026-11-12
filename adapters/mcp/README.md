# nb1 MCP adapter

The adapter exposes the nb1 API v1 through the official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk), using JSON-RPC over stdio. It connects to a running local nb1 daemon over HTTP. The daemon selects devices, compiles Actions, executes Sequences, and stores local colors and Patterns.

## Build and run

Use a complete nb1 source checkout and Go 1.25 or newer.

```sh
cd adapters/mcp
GOWORK=off go build -o nb1-mcp .
./nb1-mcp --endpoint http://127.0.0.1:8931 --timeout 10s
```

Configure an MCP host to launch that executable with those arguments. Stdout carries MCP protocol messages. Diagnostics go to stderr. The default endpoint is `http://127.0.0.1:8931`; endpoint validation accepts loopback HTTP origins.

The SDK is pinned to `github.com/modelcontextprotocol/go-sdk v1.8.0`. This nested module has its own Go 1.25 baseline. Its `github.com/b1ug/nb1 v0.0.0` requirement is replaced by `../..`, allowing the adapter to share the exact core client, strict input validation, and Job ID generator in the same checkout. Building the nested module in isolation requires that root source directory. A standalone module publication will need a published matching core dependency and removal of the relative replacement. Release binaries can be built from a complete, versioned checkout.

## Tools

| Tool | Arguments | HTTP operation |
| --- | --- | --- |
| `list_devices` | `{}` | `GET /v1/devices` |
| `get_device_state` | `serial` | `GET /v1/devices/{serial}` |
| `resolve_color` | `color` | `POST /v1/colors/resolve` |
| `preview_light` | One of `action`, `sequence`, `pattern_id`; optional `serial` | `POST /v1/preview` |
| `run_light` | `serial`, one of `action`, `sequence`, `pattern_id`; optional `id` | `POST /v1/jobs` |
| `get_job` | `id` | `GET /v1/jobs/{id}` |
| `cancel_job` | `id` | `POST /v1/jobs/{id}/cancel` |
| `list_colors` | `{}` | `GET /v1/colors` |
| `save_color` | `name`, `color` | `PUT /v1/colors/{name}` |
| `list_patterns` | `{}` | `GET /v1/patterns` |
| `get_pattern` | `id` | `GET /v1/patterns/{id}` |
| `save_pattern` | `id`, one of `action`, `sequence`; optional `title` | `PUT /v1/patterns/{id}` |

Tool schemas describe the same Action kinds, Sequence timing rules, and color forms as the [API reference](../../docs/api-v1.md). Both Action colors and authored Sequence target colors accept Hex3/Hex6, names, RGB8/HSV objects and `@local-name`; normalized Sequences and saved Patterns contain resolved lowercase Hex6 values. Input validation rejects unknown fields, duplicate keys and explicit null values. Raw color numbers and source objects retain their original JSON meaning through HTTP. The daemon owns palette resolution and authoritative compilation.

Examples below show the arguments supplied to `tools/call`.

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

Each tool returns matching JSON in `structuredContent` and its text fallback. The result contains the original `endpoint` and `data` with the corresponding HTTP response. Job operations also retain `id`; `save_color` retains `name`.

```json
{
  "endpoint":"http://127.0.0.1:8931",
  "id":"agent-task-done",
  "data":{"id":"agent-task-done","serial":"20000001","status":"running","completed_steps":0,"total_steps":4,"created_at":"2026-11-11T00:00:00Z"}
}
```

`run_light` sends one submission. An omitted or empty `id` generates `j_` followed by ten random Crockford Base32 characters before that submission. Callers can supply their own ID before running a task. A lost or malformed acceptance response returns `isError:true`, the original ID and endpoint, and `error.outcome_unknown:true`. Use `get_job` for that same ID on that same endpoint. The adapter performs no submission retry.

A definite API rejection preserves its error code and HTTP status. HTTP timeouts and MCP session closure leave accepted daemon Jobs under daemon control. Cancellation is a separate `cancel_job` operation; inspect `get_job` while status is `cancelling`.

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
