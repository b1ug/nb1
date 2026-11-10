# Experimental native reads

This source candidate adds two tools to the existing catalog: **22 candidate tools**. The installed MCP binary in this validation workspace remains at **20**. This separate MCP module has no SDK or USB dependency; the daemon's fixed SDK/GID integration is described in the [native-inspection guide](../../docs/native-inspection.md). No installation, release or device qualification is implied. All original20 complete tool objects are frozen in `testdata/native-inspection/legacy20-tools.json`.

| Tool | Required arguments | One daemon request |
| --- | --- | --- |
| `read_device_sequence_ram` | `serial`, `start_line`, `line_count` | `GET /v1/devices/{serial}/native-sequence/ram?line_count=…&start_line=…` |
| `read_device_playback_state` | `serial` | `GET /v1/devices/{serial}/native-sequence/playback` |

Serials are exactly8 hexadecimal digits. RAM requests select1–32 rows entirely within absolute positions0–31. Range numbers require ordinary integer JSON spelling; omitted fields, null, duplicates, fractions, exponents and unknown properties are rejected before HTTP. There is no first-device selection, discovery, direct USB fallback, request ticket, Job, automatic retry, stop, playback, save or library import. A fresh non-reusable HTTP transport prevents implicit GET replay after a pooled connection failure; redirects and proxies are disabled for these reads.

Every valid collection response has `accepted:false` and its full original observation under `data`. HTTP200 means that an observation envelope arrived. If its status is `partial` or `failed`, the MCP result has `isError:true` **and still retains data**, nullable fields, unknown bytes, length diagnostics and structured errors. A malformed response or admission rejection is a separate error without invented observations. Read-only describes device state: collecting R/S observations still requires feature-query reports inside the daemon.

A timeout or closed MCP session does not prove that synchronous native I/O was interrupted. This adapter never closes the device handle or automatically repeats the request.

The adapter independently checks the versioned HTTP envelope, target/range/daemon header, explicit nullable fields, raw-prefix consistency and declared interpretation. It never compiles an author recipe or creates a replacement Sequence. R reads RAM, not Flash. S exposes raw mode/count/next scheduling cursor; it does not establish the currently lit row, a forever repeat, a completed Job or optical settlement. A software/source profile remains distinct from hardware qualification.

`structuredContent` is written as a `json.RawMessage` through the official SDK's low-level tool handler, with an equivalent JSON text fallback. This avoids the generic handler's defaults/map round trip. Consumers should preserve JSON integer tokens when parsing, particularly `duration_ns`; native error codes are canonical unsigned64-bit **decimal strings**, including `"18446744073709551615"`. The adapter never interprets a missing carrier as native success or zero.

The two observation fixtures are synthetic, fixed-time outputs from the root collector: `ram-complete.json` and `playback-partial.json`. They use serial`20000001`, a Synthetic product, and explicit fixture source evidence. They are protocol vectors, not device captures or hardware validation.
