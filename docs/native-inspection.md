# Experimental native RAM and playback reads

This development candidate adds paired native reads through the existing daemon-owned device worker. It is **not installed or released**. The daily workspace remains source `6ca5a14`, with 20 MCP tools. This candidate has 22 MCP tools and two additional HTTP resources; the installed API reference's 46 canonical entries remain a separate inventory.

The SDK adapter requires the reviewed raw-read SDK candidate `b3d4a3fd8ce507884eb939288c3f7169ebf61c9b`, including its parent capability model. The checked-in `go.mod` still selects SDK b3, which does not provide the new methods. Candidate builds and tests currently use an explicit alternate module file with a local replacement; **a plain default-module build of this branch is not ready**. A reachable fixed SDK version, verified checksums and a consumer dependency change are required before integration or release. No tag, release, PR merge, installation or device qualification follows from software tests.

## Read through one owner

| Interface | RAM rows | Playback registers |
| --- | --- | --- |
| HTTP | `GET /v1/devices/{serial}/native-sequence/ram?start_line=0&line_count=32` | `GET /v1/devices/{serial}/native-sequence/playback` |
| CLI | `nb1 control native-sequence ram SERIAL --start-line 0 --line-count 32` | `nb1 control native-sequence playback SERIAL` |
| MCP | `read_device_sequence_ram` with `serial`, `start_line`, `line_count` | `read_device_playback_state` with `serial` |

The response contract is `nb1-native-inspection/experimental-v1`. Serials require exactly eight hexadecimal digits. RAM ranges explicitly select 1–32 rows within absolute positions 0–31; reading line 0 alone is valid. Playback requests accept no query parameters. No read takes a memory target, repeat, policy, request ticket, profile override, implicit save or playback option. Native CLI reads require daemon mode and reject control-only flags. Unknown devices, invalid ranges, unsupported services and busy workers are rejected before collection. There is no direct USB fallback.

The worker serializes inspection with its existing handle. An inspection is accepted only while the device is connected and has no active or queued host job. During inspection, new Program, Frame and Pattern admissions return busy, including replace requests. Reading does not stop an autonomous device player. The existing playback Job path is deliberately not reused because it performs stop/cleanup operations.

Client cancellation stops waiting; it cannot interrupt synchronous native I/O. The worker retains its handle and admission reservation until that call returns. Pending requests do not cross reconnects. The daemon instance and a per-connection session identifier bind observations to the handle that collected them. Ordinary daemon shutdown still has its own cleanup lifecycle; a read-only request is not a claim that the daemon's entire lifetime has no state-changing commands.

## Preserve observations before interpretation

Every collection envelope includes the selected device, cached-on-open firmware provenance, daemon instance, device session, UTC start/end times, a separate monotonic `duration_ns`, `source=live-native`, and `consistency=sequential-best-effort-not-atomic`.

| Field | Meaning |
| --- | --- |
| `status`, `complete` | `complete` means the requested raw observation was obtained without a collection error. `partial` and `failed` retain available data and errors. It does not mean a sequence is authorable or physically qualified. |
| `ram.rows` | Requested absolute positions, nullable raw RGB8/ticks/LED code, reports and per-row errors. On the first failure the batch stops; `unread_positions` identifies rows never queried. |
| `report` | Requested report ID, buffer length, read-attempt and returned-length facts, separate `length_valid` and `prefix_trusted`, exact available `raw_hex` and unassigned `unknown_tail_hex`. These are SDK/GID buffer observations, not a USB wire capture. |
| `ram.sequence` | Present only when the complete selected image can be represented by the independent DeviceSequence grammar. Zero-time and unfamiliar LED/flag rows remain readable even when this is null. |
| `playback.raw_fields` | Nullable raw mode, start, end, count and next scheduling cursor. No playing boolean, current lit row, forever intent or optical completion is inferred. |
| `playback.configured_range` | An optional interpretation of configuration registers using an explicit reviewed firmware end convention. It is not an active Job or recovered sequence identity. |
| `profile` | The SDK reference and firmware-specific read reference are separate. FW204 interpretation does not rewrite the SDK's FW205/model-only facts. Operation qualification remains unverified. |
| `error` | Stable code, bounded message with replacement/truncation flags, and optional native namespace/code. Native codes use canonical unsigned decimal strings to avoid integer precision loss. An absent carrier means unknown. |

A valid returned prefix remains available with an error, including a full prefix accompanied by a native failure. Such a result stays incomplete and cannot yield a deployable sequence or normalized playback range. Invalid lengths do not authenticate bytes. Missing fields are null, never padded with zeros. Every nullable field is explicit on the wire; decoders reject duplicate/unknown/missing fields, invalid Unicode, trailing data and inconsistent raw/decoded values. Responses are limited to 64 KiB.

HTTP 200 means a collection envelope arrived, **not** that collection succeeded. The CLI prints a valid incomplete envelope before returning a nonzero status. MCP sets `isError=true` while retaining the complete observation in structured content and its JSON text fallback. Neither client automatically repeats a query. Go callers receive the observation together with the original error; wrapped native causes remain accessible within the process.

## Hardware and lifetime boundaries

R reads RAM, not Flash. Raw rows contain no author title, recipe or revision and are not imported into a host library automatically. Multi-row collection is sequential, without a firmware atomic snapshot. An external USB writer is outside the daemon's process-local ownership discipline.

The reviewed FW204/205 S reply uses an exclusive end; the reviewed MK3 reply uses an inclusive end. The p write-command's end=0 sentinel must not be applied to an S reply. S reports a next scheduling cursor rather than a current illuminated row. Stopped scheduling can precede settlement of the final fade. The pure MK3 profile does not expand the current physical driver's MK2-only acceptance or prove MK3 support.

The frozen transport-trace v1 contract has no native-inspection operation/phase. This candidate explicitly returns unsupported when that trace mode is enabled, before issuing a native query. A diagnostic-contract upgrade is a separate follow-up; silently omitting new calls from a supposedly complete trace is not permitted.

No P/W/B/F, storage write, upload, playback or power-cycle operation is added. RAM upload/autonomous playback, Flash persistence, Startup and MK3 Notes remain separate implementation and qualification work. `spin`, `swap` and `switch` remain on hold.

## Local software review

For this workspace only, the explicit alternate module file is at `artifacts/native-inspection-runtime-20261110/deps/candidate.mod` under the workspace root. It replaces the SDK with the clean fixed raw-read checkout at `validation/blink1-go-raw-inspection`; the regular module files and installed binaries are unchanged. Run commands from this candidate repository, using an absolute path for `CANDIDATE_MOD`:

```sh
GOWORK=off go test -mod=readonly -modfile="$CANDIDATE_MOD" -count=1 ./...
GOWORK=off go test -mod=readonly -modfile="$CANDIDATE_MOD" -race -count=1 ./...
GOWORK=off go build -mod=readonly -modfile="$CANDIDATE_MOD" -o "$CANDIDATE_BIN" .
```

The MCP adapter is a separate module and has no SDK/USB dependency. Its [native-read guide](../adapters/mcp/NATIVE_INSPECTION.md) documents the candidate tools and synthetic protocol fixtures. Tests compose fake native readers, the real worker, HTTP, CLI/client and real stdio MCP; they are software evidence, not successful physical R/S captures.

Before installation, pin and verify a reproducible SDK module without replacements, run the corresponding software/dependency matrix, and perform a bounded FW204 R/S qualification. Retain the current installed version until those gates pass.
