# nb1 API reference: High level, Low level, and availability

Updated 2026-11-10. This reference covers the complete local HTTP surface, its CLI and MCP entry points, execution model, and host Pattern storage. It accompanies the [Chinese reference](api-reference.zh-CN.md). It does not announce a release; experimental contracts remain experimental.

GID and blink1-go are separate HID/hardware SDK layers. Their raw-report or Flash features are not nb1 HTTP endpoints. The Go interfaces in `internal/control` wire nb1 together and are not an independently importable public Go SDK.

## 1. Implemented features and installed status

| Layer | Implemented behavior | Availability and limits |
| --- | --- | --- |
| Low level: Program | Exact RGB/LED targets, fade/hold, sequential Steps, finite repeat, queue, receipts, cancellation, finish cleanup | HTTP, CLI direct/daemon, MCP; finite Mac/MK2 physical evidence. Other-OS USB, MK3, and a uniform hard deadline for blocking native calls remain unqualified. |
| Low level: FrameProgram | Overlapping two-LED fades within a Frame, one-time initialization, intervals, finite repeat, frame progress, cancellation | Experimental HTTP and internal Go executor; used by Orbit, CLI, and MCP. No stable-contract or multi-device-synchronization promise. |
| High level: light v1 | `set`, `off`, `fade`, `blink`, `breathe`, `pulse`, `alternate`, `rainbow` | HTTP/CLI/MCP/Web share the core compiler. Additional artistic actions and composition syntax are future work. |
| High level: Orbit v2 | Four/eight paired long-fade segments, colors, phase order, period, cycles, finish rule | Experimental; finite Mac/MK2 evidence across Web/CLI/MCP. No general flicker-free or geometric-rotation guarantee. |
| Host memory: Pattern v1 | Save Program/legacy Effect, import/export, exact revision references | HTTP/CLI/MCP/Web. Does not directly preserve new FrameProgram/Orbit authoring source. |
| Host memory: Pattern v2 | Original source, normalized Action, recipe, frozen typed execution, revision, execution digest | Installed6ca includes ten advanced CLI commands, MCP20 with experimental authoring, and the bilingual English-default Pattern Book. Snapshot import/export is host-only CLI; MCP snapshot clients remain unavailable. |

The prior installed workspace source was `1e2e7650e2cff6686d95cb9908953e5fd370f2bc` (1e2e765), with pinned native `bin/nb1`, `bin/nb1-runtime`, and `bin/nb1-mcp`. Its local daemon origin is `http://127.0.0.1:8931`. Installation passed **38 read-only checks**: exact binary/manifest identities, eight advanced CLI commands, actual stdio20 complete tool objects, nine English Web asset hashes/four intentionally hidden404 paths, device online/idle/black cache, clean adapter EOF, and zero mutating transport calls during verification. The Web source is `cf5219ba7c7da8e1f5db58dade22643aaa8c0861`, with11 packaged files/210608 bytes including its manifest. This reference does not itself inspect or change devices, installation, global MCP, or OS services.

The candidate's finite macOS arm64/MK2 FW204 evidence spans **two bounded windows**. CLI3/MCP2 tasks completed3/cancelled2, preserving original references and receipts; actual MCP authoring did not mutate the device. The first window remains FAILED overall because the acceptance script incorrectly expected hidden LICENSE and direct index HTTP paths to return200. A second window checked only the correct read-only English Web routes: nine hashes, four404s, three mobile English pages. The product was unchanged and the light tasks were not replayed.160 SDK attempts were paired with zero retries, failures, or trace loss in that sample. Two ordinary owned SIGTERM exits produced observed lifecycle records; logs remain best effort.

Private622-frame/30fps footage showed red/purple/blue handoff on the actual lamp. The two complete Orbit samples had179/178 internal frames without a sampled whole-lamp black interruption; peak highlight clipping was about24.7%. These observations do not certify calibrated gamma/PWM, perfect rotation, flicker-free output, long-running reliability, MK3, other-OS physical USB, or Flash/power-loss persistence. Earlier retries and exits remain separate unresolved evidence.

Installation records are local `artifacts/native-english-suite-20261110/promotion/controlled/attempt1/result.json` and `post-install/result.json`; component qualification and independent review are in the same native evidence directory. These file paths identify development evidence, not publicly downloadable API resources. Host libraries and SDK `b3f2586`/GID `37d7cfd` dependencies were retained. Source-only later English help edits are not installed in this fixed binary; legacy program messages are not all English yet. No release, OS autostart, global MCP registration, or PR merge follows from workspace promotion.

Historical provenance remains separate: former daily `1d89eae92278f24a68fff1a34e3c1e696a962c3c` had32 canonical operations/MCP12. Prior installed96a hadCLI7/MCP16 and33 installation checks, with an older seven-task window/six recovered USB retries. Its [MCP16 snapshot](mcp-tools.daily-96a.schema.json) and [MCP12 snapshot](mcp-tools.daily-1d89.schema.json) remain exact historical bytes. Earlier HTTP qualification `f8bb267` and bundle host `56c4aef` are source baselines, not missing features in the current installation. The prior1e baseline had44 canonical operations. The installed source6ca inventory has **46 canonical method/path operations**; four explicit GET aliases give50 OpenAPI operations across45 paths, with the same **111 models** and **14 JSON example files**. Five canonical operations are conditional static assets; the only additions are the two local locale JavaScript GETs.

The SDK SavePattern patch and additive hardware-capability candidate are not pinned by this documentation. Device RAM deployment, Flash persistence, startup, and Notes remain separate proposed nb1 APIs. Host storage works on MK2; device storage limits do not disable the host library.

### Current installed6ca workspace

Fixed runtime `6ca5a14e9f7fec8a5766e79f5bdef3fafef66d8f` is now installed through a separate controlled four-file promotion. Its57 read-only postinstallation checks cover exact binaries/manifest,CLI10,MCP20 complete objects,11 public Web asset hashes and hidden404 routes, healthy single owner/fresh idle dualblack state and zero application-mutation delta. The SDK b3/GID37 dependencies and user host libraries were retained. The embedded bilingual producer is `1e1b1ddf2a985dea00e494761eed592183755c1d` (13 files/260151 bytes; manifest `a35beb8fb17aa3a9e6751e006e0c7b6524d197b83cd679a25528a99134ce6460`). Three pages default to English and explicitly switch to Chinese. A separate four-check isolated browser pass verifies language navigation/roundtrip, raw JSON/titles/original session receipts, same daemon instance and no non-GET/external/mutating requests; it is read-only UI evidence, not animation or optical acceptance.

Evidence is local `artifacts/bilingual-suite-upgrade-20261110/execution-6ca-approved/result.json`, `artifacts/bilingual-suite-upgrade-20261110/execution-6ca-approved/post-install/result.json` and `artifacts/bilingual-suite-upgrade-20261110/execution-6ca-approved/browser-read-only/result.json`. Documentation itself performs no HTTP, USB, service, browser or installation operation. Previous1e/CLI8/English Web and its38-check promotion remain historical. No release, global MCP registration, OS startup change or new MK3/Flash/other-OS physical qualification follows.

## 2. High level and Low level responsibilities

**High level describes the desired effect; Low level describes its execution.** Both remain available and share a device owner and worker.

```text
Web / CLI / MCP / HTTP
          |
          +-- light Action: blink / breathe / pulse / orbit ...
          |       +-- nb1 core compilation
          |
          +-- exact Program / FrameProgram
                         |
                shared device queue and receipts
                         |
                  SDK -> GID -> USB
```

An advanced request is compiled, queued, and timed by nb1; callers do not need to send a loop of low-level requests. The device performs fades while the host sequences targets and intervals. USB writes remain ordered, so this does not upload arbitrary advanced animation for autonomous hardware playback.

Basic `set/off/fade` are light Actions too. Use Program when individual Steps matter, and FrameProgram when two LEDs need overlapping fades in one phase. Expressions such as `light.breathe({...})` are explanatory notation, not a promised JavaScript SDK. Actual HTTP input uses explicit `contract` and `action`; CLI reads the same JSON and MCP offers `preview_light/run_light`.

## 3. Common calling rules

- Default HTTP origin: `http://127.0.0.1:8931`. The server enforces loopback Host and matching browser Origin. It provides no authenticated public/cloud API or relaxed CORS.
- Send JSON with `Content-Type: application/json` and case-sensitive canonical field names. Strict boundaries reject unknown/duplicate fields, explicit `null`, and trailing values. Size, nesting, numeric, and Unicode rules vary by model; do not apply every newer-codec guarantee to legacy routes.
- Obtain the real `serial` from the configured device list. `20005601` is only an example. New validators accept 1–128 ASCII letters, digits, `-`, or `_`; a device still must be configured and actually have that serial. Individual clients can impose narrower shapes, including MCP's eight-hex-digit serial.
- Times are milliseconds; colors are raw RGB8 without implicit software Gamma or brightness calibration. Nominal duration excludes some native USB and OS scheduling overhead.
- `HTTP 202` means accepted. A trusted terminal Job establishes execution/cleanup outcome; device readback and optical observation are separate evidence.
- CLI `control` defaults to daemon mode. Explicit `--mode direct` uses the same core and ownership lock in-process; a failed daemon connection never automatically falls back to direct USB.

`control --timeout` defaults to 30s. Prepare, submission, and `--wait` share one context budget. Set a longer timeout for a long task, for example `nb1 control --timeout 2m patterns-v2 run SERIAL ID --revision SHA --wait`. A daemon task may continue after client timeout. Preserve the original ticket and explicitly recover through its receipt rather than generating a new key.

Pure preview creates no Job, execution ticket, or USB operation. Optional `serial` checks only a configured cached snapshot. It reserves neither the device nor future admission capacity.

## 4. High level: light v1

```json
{
  "contract": "nb1-light-action/v1",
  "action": {
    "kind": "breathe",
    "color": "lime",
    "led": 0,
    "period_ms": 2000,
    "cycles": 2,
    "on_finish": "off"
  }
}
```

Author files contain only `contract/action`. HTTP preview may add `serial`; execution also supplies `policy/request_id/expected_daemon_instance_id`.

Every Action accepts `on_finish: off|hold`. Defaults are `hold` for set/off/fade and `off` otherwise. Except alternate, `led` defaults to 0 and accepts 0/1/2; 0 is the firmware's common output selector.

| kind | Meaning | Fields, defaults, bounds | Compiled nominal duration |
| --- | --- | --- | --- |
| `set` | Set a color immediately | Required `color` | 10ms |
| `off` | Set selected output black | Does not accept color | 10ms |
| `fade` | Fade to a target | Required color and `duration_ms`; 10–600000, multiple of 10ms | duration+10ms |
| `blink` | On/off edges without fades | Required color; `on_ms:250`, `off_ms:250`, each 10–3600000; `cycles:1` | (on+off)*cycles |
| `breathe` | Symmetric smooth intensity rise/fall | Required color; `period_ms:2000`, 330–1200000; `cycles:1` | period*cycles |
| `pulse` | Fast attack, decay, rest | Required color; `attack_ms:60`, `decay_ms:240`, each 10–600000 and multiple of 10ms; `rest_ms:700`, 0–3600000; `cycles:1` | (attack+decay+rest+30)*cycles |
| `alternate` | Exchange two colors across LEDs | `colors:[red,blue]`; `leds:[1,2]` or [2, 1]; `period_ms:2000`; `cycles:1`; optional transition | period*cycles |
| `rainbow` | Red/yellow/green/cyan/blue/magenta hue cycle | No color/colors; `period_ms:3000`, 130–1200000; `cycles:1` | period*cycles |

`cycles` syntax permits 1–10000, but v1 light is limited to **1024 total executed Steps**, with at most 24 hours nominal duration after compilation. Fields belong to their Action: breathe rejects attack_ms, and rainbow rejects color.

Advanced Action times/counts require ordinary nonnegative JSON integers. `1.0`, `1e3`, negative zero, and numeric strings are rejected. Each request is at most 65536 bytes; authored JSON nesting is at most 32. HSV fractions follow separate color rules.

### Alternate compatibility

Only alternate accepts `transition`. Omission or `cut` retains the original four-Step exchange with period 40–1200000ms. `linear/sine/orbit` retain the old experimental 66-Step/cycle form, period 1300–1200000ms, at most 15 cycles.

Legacy `alternate.transition:orbit` and the new `kind:orbit` have different execution semantics. Old files, Programs, and stored Patterns do not auto-upgrade. `alternate` remains the implemented name; switch/swap/spin/sequence/compose are not callable action kinds.

### Finish output

`hold` retains the final Step output. blink/breathe/pulse usually finish black; rainbow finishes red; cut alternate retains exchanged colors. It does not mean holding the input color. `off` performs output-off cleanup. Finish/active-cancel off cleanup is device-wide, so it cannot promise to affect only the Action's selected LED.

## 5. High level: experimental Orbit v2

```json
{
  "contract": "nb1-light-action/experimental-v2",
  "action": {
    "kind": "orbit",
    "colors": ["red", "blue"],
    "leds": [1, 2],
    "period_ms": 3000,
    "cycles": 2,
    "segments": 4,
    "on_finish": "off"
  }
}
```

| Field | Default and rules |
| --- | --- |
| kind | Exactly orbit; v2 does not automatically accept v1 Actions |
| colors | red/blue; exactly two ColorInput values |
| leds | [1, 2], or [2, 1]; logical phase order is not proof of perceptible clockwise/counterclockwise rotation |
| period_ms | 3000; each segment interval 50–600000; total 200–2400000 for four segments, 400–4800000 for eight |
| cycles | 1; 1–256 for four segments, 1–128 for eight; total nominal duration at most 24 hours |
| segments | 4; only 4 or 8 |
| on_finish | off, or hold |

Preview returns normalized Action, `recipe_id`, frozen FrameProgram, total Frames, planned target writes, and nominal duration. The four-segment recipe is `orbit-paired-linear-4/experimental-v1`; eight uses the corresponding `-8/` recipe.

Fixed curve samples produce targets connected by long device-linear fades. Two LED USB writes have a 10ms sequencing allowance. Initialization happens once; repeat repeats only Frames. Fade rounds upward to 10ms, and final cleanup/readback waits for the final fade. The default 3000ms one-cycle plan is nominally 3040ms with four segments or 3045ms with eight, including initialization/rounding/wait overhead. Those values are not measured wall-clock promises. Housing, PWM, exposure, and parameters still affect appearance.

## 6. ColorInput and exact execution colors

High-level light and `resolve_color` accept the forms below. Legacy Program/Effect, CLI `control set`, and MCP `set_device_state` still require `#RRGGBB` execution colors. High-level `light.kind:set` accepts full ColorInput.

| Form | Example | Result/rules |
| --- | --- | --- |
| Hex6 | `"#12AbEF"` | `#12abef` |
| Hex3 | `"#0f8"` | `#00ff88` |
| Name | `"green"`, `"lime"` | Fixed148 opaque CSS names; respectively #008000 and #00ff00 |
| RGB8 | `{"space":"rgb8","r":0,"g":255,"b":136}` | Ordinary integer channels 0–255; no alpha |
| HSV | `{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":50}` | h0–360, s/v0–100; result#008000 |

HSV accepts ordinary nonnegative decimal integers or fractions with at most six decimal places, without exponents, negative zero, NaN, or Infinity.360 degrees equals 0. Exact decimal conversion uses one final half-up RGB8 quantization.

Names are ASCII case-insensitive with surrounding ASCII whitespace stripped. A string is at most 64 decoded bytes; standalone color requests are at most 4096 bytes. There are no rgb() strings, HSL, alpha, transparent, currentColor, Chinese aliases, CSS variables, random, or user palettes. Out-of-range values are rejected rather than clamped.

```http
POST /v1/colors/resolve
Content-Type: application/json

{"color":{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":50}}
```

```json
{"schema_version":1,"color_contract":"nb1-color-input/v1","input_kind":"hsv","canonical_color":"#008000","rgb8":{"r":0,"g":128,"b":0}}
```

Only named-color results include `palette:css-named-2026`. Resolution does not light the device. Pass `canonical_color` to a low-level Program when needed. Legacy SDK palette/Gamma settings do not guarantee the same result as this contract.

## 7. Low level: Program

Program JSON has no new contract wrapper. Capabilities/preview declare `compile_contract:nb1-program-effect/v1`.

```json
{
  "steps": [
    {"led":1,"color":"#ff0000","fade_ms":400,"hold_ms":600},
    {"led":2,"color":"#0000ff","fade_ms":400,"hold_ms":600}
  ],
  "repeat":2,
  "on_finish":"off"
}
```

| Field | Rules |
| --- | --- |
| steps | 1–1024 defined sequential Steps |
| led | 0/1/2; omission defaults to 0 |
| color | Required Hex6; uppercase normalizes to lowercase; no names/Hex3/HSV |
| fade_ms | 0; 0–600000, multiple of 10ms |
| hold_ms | 0; 0–3600000 |
| repeat | Omission or 0 normalizes to 1; maximum 10000; 0 is not infinite |
| on_finish | Omission or empty string normalizes to off; also accepts hold; canonical output is off/hold |

Each Step nominally waits `fade_ms+hold_ms+10ms`. This example takes 2020ms per cycle, 4040ms for two, plus USB/cleanup overhead. Program's1024 limit counts **defined Steps**, unlike light v1's1024 **executed Steps**. Total nominal duration is at most 24 hours.

Legacy Program/Effect HTTP decodes through Go int: fractional/exponent spelling is rejected, but `-0` becomes 0 before range/default handling. Do not extend the advanced Action/Pattern author's raw-negative-zero rejection to that older boundary. New callers should use ordinary nonnegative integer literals.

Program is sequential: one Step waits before the next starts. Two separate LED Steps do not overlap fades; use FrameProgram or compiled Orbit. Preview lists set/program/raw-rgb and optional hold capabilities. Legacy Submit does not uniformly enforce every advertised preview capability, so the catalog is not a blanket admission guarantee.

Preview: `POST /v1/programs/preview` with `{"program":PROGRAM,"serial":"OPTIONAL_REAL_SERIAL"}`. Run: `POST /v1/devices/{serial}/jobs` with Program and explicit policy, optionally the complete receipt pair. **There is no separate HTTP /set or /state route.** Read `GET /v1/devices/{serial}`. CLI set constructs a one-Step hold Program and submits jobs.

## 8. Low level: experimental FrameProgram

```json
{
  "contract":"nb1-frame-program/experimental-v1",
  "initial":[{"led":1,"color":"#ff0000"},{"led":2,"color":"#0000ff"}],
  "frames":[
    {"targets":[{"led":1,"color":"#0000ff"},{"led":2,"color":"#ff0000"}],"fade_ms":500,"interval_ms":500},
    {"targets":[{"led":1,"color":"#ff0000"},{"led":2,"color":"#0000ff"}],"fade_ms":500,"interval_ms":500}
  ],
  "repeat":2,
  "on_finish":"off"
}
```

| Field | Rules |
| --- | --- |
| contract | Exactly nb1-frame-program/experimental-v1 |
| initial | 1–2 distinct physical LED1/2 targets; executed once |
| frames | 1–1024 defined Frames; target count and LED order match initial |
| targets[].color | Raw Hex6 |
| fade_ms | Required in HTTP; explicit 0 accepted; 0–600000, multiple of 10ms, no greater than interval rounded upward to 10ms |
| interval_ms | Required in HTTP; 50–600000 |
| repeat | HTTP omission defaults to 1; explicit 1–10000; total executed Frames at most 1024; explicit 0 rejected |
| on_finish | off by default, or hold |

At most 2050 total target writes and 24 hours nominal duration; input at most 1MiB. Each Frame sets targets sequentially, then waits the remaining shared interval, rather than waiting a whole fade separately for each LED. Late execution does not burst through catch-up Frames. Completion waits for the final fade.

Internal typed `NormalizeFrameProgram` may default zero repeat/empty on_finish. The HTTP decoder defaults only omitted repeat/on_finish and rejects explicit repeat 0 or empty on_finish. Each target's led/color and each Frame's fade_ms/interval_ms are required. Raw Frame integers still use Go int, so fade_ms:-0 equals 0; Orbit/Pattern authoring have stricter lexical rules. Internal typed tolerance is not HTTP syntax.

Preview uses `/v1/frame-programs/preview` with program and optional serial; run uses `/v1/devices/{serial}/frame-jobs`. Raw Frame and current high-level light execution require explicit reject and the complete original ticket. CLI/MCP reach the Frame executor through Orbit; no independent raw-Frame command/tool exists.

Read `GET /v1/frame-capabilities` separately from v1 capabilities. Current cached gates require set/raw-rgb/mk2-basic. The old helper lists hold for preview but does not independently gate it; Pattern v2 Frame-hold admission adds that gate. Firmware version alone does not qualify MK3 hardware.

## 9. Legacy Effect compatibility

Effect is shorthand for the old jobs and `preview_program/run_program` boundary, not the full light Action language.

| kind | Input/semantics |
| --- | --- |
| steady | Required kind/color; optional led/fade_ms; ends hold; rejects cycles/period_ms/on_finish |
| pulse | Required kind/color; optional led, cycles default 1, period_ms default 1000 (60–1200000, multiple of 10ms), on_finish default off; rejects fade_ms |

Legacy Effect pulse is a symmetric fade up/down. New light pulse is attack/decay/rest; new breathe is symmetric breathing. Preserve the entry point and contract when interpreting old Programs/Patterns.

```json
{"effect":{"kind":"pulse","color":"#00ff00","period_ms":1000,"cycles":2}}
```

`/v1/programs/preview` requires exactly one program/effect/pattern_ref. A structural dry_run is neither authoritative compilation nor execution support.

## 10. Preview, original receipts, Jobs, and cancellation

`daemon_instance_id` identifies one process lifetime; `request_id` identifies one submission. They prevent a lost response from redirecting an old intent into a restarted process. CLI prepares once automatically; MCP callers retain the actual prepared pair; Web retains it for the operation. These are neither accounts, serials, permanent Pattern IDs, nor a persistent task database. Preparation does not light/reserve the device or reserve receipt capacity.

1. Select serial from `GET /v1/devices`; read capabilities/cache as needed.
2. Call the matching pure preview; inspect normalization, capability findings, and nominal budget.
3. Read `/v1/instance`; append 32 random lowercase-hex characters to `idempotency.request_id_prefix`, then preserve that key and daemon instance together.
4. Submit the same original intent and ticket once. Light v1 uses `/v1/devices/{serial}/light`; Orbit uses `/v2/devices/{serial}/light`.
5. Verify202, exactly one matching instance header, exactly one replay header, and full Job identity. Query the original receipt to terminal state.

Headers are `X-NB1-Daemon-Instance-ID`, `X-NB1-Replayed:true|false`, and Location pointing to the original receipt. Admission deadlines use Engine monotonic uptime, not wall-clock date. Do not hardcode expired sample keys.

| Execution | Original receipt GET | ID GET | Cancellation POST |
| --- | --- | --- | --- |
| Program | `/v1/devices/{serial}/requests/{request_id}?expected_daemon_instance_id=ORIGINAL_INSTANCE` | `/v1/jobs/{id}` | `/v1/jobs/{id}/cancel` |
| Frame/Orbit | `/v1/devices/{serial}/frame-requests/{request_id}?expected_daemon_instance_id=ORIGINAL_INSTANCE` | `/v1/frame-jobs/{id}` | `/v1/frame-jobs/{id}/cancel` |

Frame cancel may include expected_daemon_instance_id and validates it first. Advanced CLI v2 cancel also checks original Action/ticket. Legacy Web/general MCP cancel_job ID cancellation does not have identical complete binding. Pattern v2 uses its own original Ref/ticket-bound routes.

Within one instance and bounded retention, the same serial/key/canonical content replays the original Job, without creating another Job; conflicting content fails. This does not make each USB instruction exactly once, persist across restart, or guarantee accepted work succeeds.

Save [breathe.action.json](examples/api/breathe.action.json), then preview without playing:

```sh
curl -sS -X POST http://127.0.0.1:8931/v1/light/preview \
  -H 'Content-Type: application/json' --data-binary @breathe.action.json
```

To play deliberately, preserve one actual ticket and build its run envelope:

```sh
nb1 control prepare > original-ticket.json
python3 - <<'PY'
import json
from pathlib import Path
intent = json.loads(Path('breathe.action.json').read_text())
ticket = json.loads(Path('original-ticket.json').read_text())
request = {**intent, 'policy': 'reject', **ticket}
Path('submit.json').write_text(json.dumps(request))
PY
# This POST plays; replace 20005601 with the selected real serial.
curl -sS -D accepted.headers -o accepted.job.json \
  -X POST http://127.0.0.1:8931/v1/devices/20005601/light \
  -H 'Content-Type: application/json' --data-binary @submit.json
```

Preserve full response headers and the original ticket, then query that receipt. This minimal curl example illustrates the flow; automation must validate status, unique headers, Job identity, and lifecycle/progress. A successful curl exit is not Job completion. Orbit uses v2 preview/run paths with its unchanged authored input.

```text
queued -> running -> succeeded
             |  +-> failed
             +-> cancelling -> cancelled or failed
queued -> cancelled
```

Program progress uses completed_steps/total_steps. Frame uses completed_frames/total_frames and completed_target_writes/planned_target_writes. Full counts may precede the final fade wait/cleanup. cancelling is in progress; cancelled means the worker confirmed required cleanup, while cleanup failure becomes failed.

| Object | Fields | Meaning |
| --- | --- | --- |
| DeviceState | serial/product/path?/firmware?/capabilities | Identity/cache; path is not a client arbitrary-file API |
| DeviceState | connection/active_job?/queue_depth/last_error? | Connectivity, current work, queue, error |
| DeviceState | desired? | Latest logical target Step, not measured optical color |
| DeviceState | observed? | led1/led2/sampled_at; cached, not a fresh USB read per GET |
| Job/FrameJob | id/device/status/program | Original identity, serial, lifecycle, frozen execution |
| Job | completed_steps/total_steps | Sequential progress including repeat |
| FrameJob | completed_frames/total_frames | Complete-frame progress |
| FrameJob | completed_target_writes/planned_target_writes | Target writes, excluding other USB commands such as Read/Stop |
| Job/FrameJob | created_at/started_at?/finished_at?/error? | RFC3339 timestamps/error; absent optional fields are omitted rather than null |

Legacy Program/light/Frame HTTP returns bare Job/FrameJob. Pattern v2 returns PatternJobEnvelope with original Ref/execution digest. CLI usually nests under job; MCP uses structuredContent.data. Program preview includes schema_version/compile_contract/input_kind/program/nominal_duration_ms/total_steps/required_capabilities/validation/device?. Advanced preview adds normalized Action and compiled execution; v2 adds recipe/frame statistics. `supported` and `cached_profile_supported` describe cache matches, not admission or physical effects.

Cancelling an already succeeded hold does not turn off the device: issue explicit off if intended. Client timeout, MCP exit, or closing Web does not automatically cancel daemon work. Go timeout cannot forcibly release a synchronous native call. Cancelling a queued Job performs no new device write; another active Job or old hold output may remain. Thus cancelled does not universally mean the entire device is black.

After disconnect, timeout, response identity failure, expired receipt, or instance change, retain original serial, intent, ticket, and last trustworthy Job. Query only that original receipt; do not generate another key to play again.404 means absent at query time and does not prove nothing executed. A later valid-looking response must not erase an integrity failure. Pure preview failure creates no unknown execution task. Save `publication_unknown` and execution `outcome_unknown` are separate outcomes.

## 11. Scheduling policy differences

| Policy | Ordinary Program jobs | Current light/raw Frame | Pattern v2 HTTP |
| --- | --- | --- | --- |
| replace | Implemented; old CLI default | Rejected | Implemented; explicit |
| queue | Implemented; bounded queue | Rejected | Implemented; explicit |
| reject | Implemented | Required explicitly or selected explicitly by advanced CLI/MCP | Implemented; explicit |

These are boundary differences around the same per-device worker, not independent executors. Program/Frame share device ownership. Pattern HTTP/MCP run_pattern require an explicit policy. CLI patterns-v2 run has effective default reject and explicitly accepts replace/queue/reject without changing the old parent CLI replace default.

## 12. Host Pattern memory

Pattern v2 requires ASCII object keys at every level, including nested unknown source. Unicode string values remain valid. Preserve exact integer spelling and int64 bounds; do not round-trip through JavaScript Number or a generic float64 object. Unknown-source support does not admit arbitrary JSON.

### Pattern v1

The bounded host create-only library holds at most 128 records/1MiB. It stores Program/legacy Effect authored packages and frozen snapshots, supports list/get/import/export and fixed id+revision preview/run. Import does not play, different content cannot overwrite the same ID, and latest is never implicit.

HTTP/CLI/MCP/Web are integrated. MCP provides list/get and fixed-reference preview/run, without import tools. New FrameProgram and source.action do not fit this v1 source model. Saving a compiled light Program reuses its output while losing advanced authoring intent; it is not complete high-level Pattern preservation.

### Pattern v2 authoring and storage

The following is the unchanged authored example data, including its Unicode title:

```json
{
  "id":"orbit-red-blue",
  "title":"红蓝环绕",
  "source":{
    "kind":"light_action",
    "contract":"nb1-light-action/experimental-v2",
    "action":{"kind":"orbit","colors":["red","blue"],"period_ms":3000,"cycles":2,"segments":4,"on_finish":"off"}
  }
}
```

Preview builds the complete Record: record_contract/id/title/source/compiled.normalized_action, optional recipe_id, typed execution, and revision. Source preserves the JSON meaning of authored color notation and omitted defaults, not original whitespace bytes.

Save adds `expected_revision` to the same author request, confirms the complete candidate, then performs one create-only Import. This is not overwrite permission or a current-library version, and it needs no daemon ID. New record returns 201; identical existing record returns 200; neither returns 202 nor a Job. An edit such as a changed period must use a new ID: retained editable source does not provide an overwrite operation or version tree.

`execution_digest` binds only frozen execution. Different authored inputs may share that digest but have different revision; revision binds the full record. They cannot replace each other. Integrity-valid unknown source/recipe records can be displayed/exported read-only, but cannot be newly imported or executed. An unknown record makes library writes read_only; known individual records may still be validated for execution. The v2 library is bounded to 128 records and 1MiB canonical package/snapshot, independently of v1.

P3 exposes authored preview, conditional save, list/get, frozen snapshot export/import. The daemon selects independent lazy `UserConfigDir()/nb1/patterns-v2`, overridden by `--pattern-v2-dir` or `NB1_PATTERN_V2_DIR`. Old --pattern-dir/NB1_PATTERN_DIR select only v1; custom v1 paths do not redirect v2. Handler construction, pure authored preview, and absent-library reads create no directory. A valid save/import entering the write transaction may create a directory even if later publication fails; reading an existing library may create a lock file. Configuration errors do not migrate, fall back, or overwrite the old library. Go handlers without v2 options return 501 for storage; configuration errors return 503; pure author preview always has zero Store calls.

P4 verifies typed identity/digest atomically with admission; P5 revalidates source/recipe/frozen execution/revision; P6 exposes fixed-reference execution. The core also checks typed execution/digest/Ref against the original ticket. Sending frozen Pattern output through a bare Program route does not preserve managed source identity.

| P6 operation | Input and behavior |
| --- | --- |
| POST `/v2/patterns/preview-ref` | Complete pattern_ref, optional serial; validate stored source, then return frozen execution and Program/Frame preview. At most one cached device read; no Job/ticket. |
| POST `/v2/devices/{serial}/pattern-jobs` | Complete Ref, explicit replace/queue/reject, original request_id/expected_daemon_instance_id; 202 PatternJobEnvelope with unique instance/replayed/Location headers. |
| GET `/v2/devices/{serial}/pattern-requests/{request_id}` | Exactly four unique query fields: expected_daemon_instance_id, record_contract, pattern_id, revision; 200 original Envelope; no Store/config lookup. |
| POST `/v2/devices/{serial}/pattern-requests/{request_id}/cancel` | Ref, expected_daemon_instance_id, optional confirmed_job_id. Only omission removes Job-ID constraint; empty/null rejected.202 may still be cancelling/terminal; no Store/config lookup. |

Do not precheck Store before the core's receipt lookup. Only a new key lazily resolves source. Exact accepted receipts remain replayable/queryable/cancellable under core rules when the library disappears, configuration fails, or Engine closes. Preserve the original ticket despite library failure. Envelope is `{pattern_ref,execution_digest,job_kind,job}`; the typed Job/FrameJob shapes are unchanged. Bare legacy getters hide Pattern source and are not recovery fallbacks. Location already URL-encodes the full Ref/original instance. Terminal hold cancellation does not issue off.

`pattern_execution.available` only describes PatternService/resolver wiring and makes zero Store calls. True reason is pattern_service_configured; false reasons are pattern_service_unsupported, pattern_library_unconfigured, or pattern_library_configuration_error. True proves neither library health nor admission; false does not block accepted-receipt recovery. New Pattern Frame-hold admission additionally checks cached hold capability, unlike the legacy Frame helper.

P6 POST has a 4096-byte raw limit, one application/json content type, no query, strict UTF8/duplicate/unknown/null rejection, and closed ASCII identity scalars. GET requires four unique nonempty parameters, no unknown/empty segments, and no body. Hash spelling alone is not source verification. JSON Schema cannot prove hashes or receipt binding.

Extract a fixed Ref from an actual saved Record for read-only preview:

```sh
NB1_ORIGIN=http://127.0.0.1:8931
PATTERN_ID=orbit-red-blue
record=$(curl --fail --silent "$NB1_ORIGIN/v2/patterns/$PATTERN_ID")
ref=$(printf '%s' "$record" | jq -c '.record | {record_contract,id,revision}')
jq -n --argjson pattern_ref "$ref" '{pattern_ref:$pattern_ref}' |
  curl --fail --silent -H 'Content-Type: application/json' --data-binary @- "$NB1_ORIGIN/v2/patterns/preview-ref"
```

Execution still needs one actual prepared original ticket. Preserve full Ref/ticket/Location and submit once. Network/context failure does not prove non-acceptance. Host library storage, short-lived task receipts, device RAM playback slots, and hardware Flash are separate. No automatic replay or Flash write is implied; MK2 can use computer-side advanced Pattern storage.

## 13. CLI and MCP entry points

| Purpose | CLI (after `nb1`) | MCP |
| --- | --- | --- |
| Discover/prepare | control devices / control prepare | list_devices, optionally prepare_request |
| Read cache | control state SERIAL | get_device_state |
| Resolve color | control color INPUT / --file FILE | resolve_color |
| Preview light | control light preview FILE | preview_light |
| Run light | control light run SERIAL FILE --wait | run_light |
| Program/Effect preview | control preview --program FILE / --effect FILE | preview_program |
| Program run | control run SERIAL FILE | run_program |
| Legacy Effect run | control effect SERIAL FILE | run_program effect branch |
| Exact single-Step set | control set SERIAL COLOR | set_device_state |
| Read/cancel Job | control job ID / control cancel ID | get_job / cancel_job |
| v1 host library | control patterns list/get/import/export/preview/run | list_patterns/get_pattern; preview_program/run_program pattern_ref branch |

MCP is a real stdio protocol adapter to local HTTP. It does not spawn CLI subprocesses or own USB. Historical1d89 has 12 tools: cancel_job/get_device_state/get_job/get_pattern/list_devices/list_patterns/preview_light/preview_program/resolve_color/run_light/run_program/set_device_state. Installed6ca5a14 retains the same20 complete objects captured from1e; the [current catalog](mcp-tools.schema.json), [previous16 snapshot](mcp-tools.daily-96a.schema.json), and [historical12 snapshot](mcp-tools.daily-1d89.schema.json) are retained. The complete old16 objects remain unchanged in the current catalog. Real session tools/list remains authoritative.

Use the [example files](examples/api/README.md). Outside the workspace, choose a verified binary path; installed workspace `bin/nb1` is pinned1e2e765. These commands demonstrate local preview, daemon/cache preview, and deliberate run:

```sh
nb1 control --mode direct light preview breathe.action.json
nb1 control light preview breathe.action.json --serial 20005601
nb1 control light run 20005601 breathe.action.json --wait
```

Advanced CLI can prepare once, or accept the full original pair saved by control prepare. MCP run_light requires selected serial, original ticket, and reject. get_job distinguishes Program/Frame by ID prefix; when only a ticket is known, Frame needs explicit job_kind:frame. MCP does not expand animation or retry execution.

### Ten installed advanced Pattern CLI commands

| `control patterns-v2` command | Behavior and bounds |
| --- | --- |
| list | One bounded advanced-summary GET; no per-record reads, preview, ticket or playback |
| get ID | Complete stored Record; valid unknown remains read-only |
| preview-draft FILE | Pure `{id,title,source}` preview; no save/ticket |
| save FILE | Author input plus expected_revision; original Ref and publication_confirmed/publication_unknown; no playback |
| preview ID --revision SHA [--serial SERIAL] | Fixed stored Ref preview; optional cache echo; no Job/ticket |
| run SERIAL ID --revision SHA | Effective default reject; explicit --policy replace/queue/reject. Prepare once if no pair, zero Prepare with original pair; one execution POST; no implicit Get/preview |
| request SERIAL ID --revision SHA --request-id KEY --expected-daemon-instance-id INSTANCE | Original receipt only; optional --job-id/--execution-digest constraints; no library read/rebinding |
| cancel SERIAL ID --revision SHA --request-id KEY --expected-daemon-instance-id INSTANCE | GET/validate original receipt, then at most one cancel POST with confirmed_job_id; terminal/cancelling means no POST; optional original Job/digest constraints |
| import FILE | Complete bounded snapshot verified locally; original bytes once to create-only host storage; publication uncertainty retained; no playback/ticket |
| export [ID] [--output FILE] | One integrity-checked host snapshot GET; preserves valid future recipes; stdout or exclusively created0600 file; no overwrite/playback |

SHA must be the real full sha256: 64-lowercase-hex revision; no latest. record_contract is fixed experimental-v2 and retained in output. All ten are daemon-only; direct is rejected. Preview/author commands do not accept policy/wait/tickets. request/cancel require the original pair and reject policy. Installed6ca includes list/import/export; previous1e had the first eight commands. The old patterns command does not implicitly understand v2.

The installed eighth command `control patterns-v2 list` makes one bounded GET for summaries, without fetching every Record, preview, prepare, run, or opening the reported path. Valid unknown source/recipes remain visible read-only. POSIX/drive/complete UNC paths follow server path syntax. Summary verification is advertised metadata; use get for complete independent Record/source/revision/digest verification. Installed6ca includes explicit host-only snapshot import/export; historical1e did not. Import validates a complete snapshot then sends its original bytes once to the existing create-only route, without playback/tickets. Export checks a bounded complete snapshot and writes stdout or an exclusively created0600 output; known and valid future recipes remain distinct. Keep `publication_unknown` and the original snapshot/Refs for read-only reconciliation; never automatically re-import. MCP import/export remains unavailable.

run/request/cancel support --wait within the shared timeout budget (default 30s). Waiting queries only the original Pattern receipt and checks typed Program/Frame, Ref, digest, Job ID, creation/start timestamps, progress, and lifecycle. Timeout does not cancel/resubmit or guess from history. A validated terminal state ends waiting before a subsequent caller-context stop can turn it unknown. run/request may exit nonzero for failed/cancelled while retaining the trusted terminal result and outcome_unknown=false; a failed Job may validly omit error text.

Output retains pattern_ref/request_ticket/accepted/outcome_unknown/daemon_instance_id/replayed/job. job is the PatternJobEnvelope, containing the typed Job. accepted means confirmed admission, not completion. cancel_attempted means only that the single cancel POST was attempted, not delivered or cleaned up. Lost/bad cancel responses preserve the original ticket and last trustworthy before snapshot and return error immediately, even with --wait; a later valid GET does not mask corruption. The caller may explicitly request the original receipt afterward.

Only explicit status/code pairs establish this admission was rejected: 400 invalid_pattern_request/invalid_request; 404 pattern_not_found; 409 busy/queue_full/idempotency_conflict/pattern_revision_conflict/pattern_library_busy; 429 idempotency_capacity. Generic404, unknown code/wrong status, 3xx, 5xx/501, expiration, instance change, or transport/parse failure retain unknown and do not authorize another ticket. Query/cancel errors do not prove original execution never happened.

```sh
nb1 control patterns-v2 get orbit-red-blue
nb1 control patterns-v2 preview orbit-red-blue --revision "$REVISION" --serial "$SERIAL"
nb1 control patterns-v2 run "$SERIAL" orbit-red-blue --revision "$REVISION" --policy reject --wait
nb1 control patterns-v2 request "$SERIAL" orbit-red-blue --revision "$REVISION" --request-id "$REQUEST_ID" --expected-daemon-instance-id "$INSTANCE"
nb1 control patterns-v2 cancel "$SERIAL" orbit-red-blue --revision "$REVISION" --request-id "$REQUEST_ID" --expected-daemon-instance-id "$INSTANCE" --job-id "$JOB_ID" --execution-digest "$EXECUTION_DIGEST" --wait
```

These variables must come from the actual selected device, saved Record, and original ticket. Clients recompute the closed execution digest and receipt binding, not authored-source semantics before run; full source/recipe/revision verification remains daemon P5/core work. See the [localized CLI details](patterns-cli.md) and [execution client design](../internal/daemonclient/README.pattern-execution-v2.zh-CN.md).

### Four installed advanced Pattern MCP tools

| Tool | Required inputs | Behavior |
| --- | --- | --- |
| preview_pattern | pattern_ref; optional eight-hex serial | One Ref-preview POST; no prepare/Job; accepted:false |
| run_pattern | pattern_ref,serial,policy,request_id,expected_daemon_instance_id | Explicit replace/queue/reject; one execution POST; no implicit preview, library preflight, Prepare, or retry |
| get_pattern_request | pattern_ref,serial,request_id,expected_daemon_instance_id | Read original receipt; optional known job_id/execution_digest constraints |
| cancel_pattern_request | Same as get, with optional same constraints | GET/validate original receipt, then at most one cancel POST with confirmed_job_id; terminal/cancelling means no POST |

Ref contains record_contract:`nb1-pattern-record/experimental-v2`, ID, and full revision from the original Record. Use existing list_devices({prepare_request:true}) once for the original ticket. Library deletion/read failure must not prevent accepted-receipt recovery. Use Pattern tools for these tasks; falling back to get_job/cancel_job loses source binding.

Successful run returns structuredContent.kind=pattern_job, data=`{pattern_ref,execution_digest,job_kind,job}`, and outer request_ticket. Query kind is pattern_request; cancel kind is pattern_cancel with data.pattern_job and cancel_attempted. Cancel errors retain original ticket/last trustworthy snapshot. Unknown outcomes never trigger a new key, and later good responses do not conceal integrity failures.

The 16-tool snapshot provenance is clean `c37f7fc33d52cc25619b10a5bc477965bf544cc0`; `679bdfc735a2f011c4c1fbf4fabec80f406f8f18` adds error-header checks without changing objects. Historical software stdio tests used temporary Store/memory Engine rather than USB. Prior96a physical evidence is separate. The MCP module keeps Go1.25 minimum; root Go1.18 compatibility does not substitute. The unchanged authoring tools retained in installed6ca are described below; snapshot import/export MCP is not installed. Legacy list_patterns/get_pattern remain v1.

### Installed MCP20 experimental host authoring

Installed6ca retains four tools first integrated in1e from `10cf37f`: list_patterns_v2/get_pattern_v2/preview_pattern_draft_v2/save_pattern_v2. They list summaries, read full Record, preview source, or conditionally save expected_revision. They neither prepare execution tickets nor create/play Jobs, and do not change old v1 tool meaning. [Current20 schemas](mcp-tools.schema.json) are the exact captured1e objects, independently matched by installed6ca tools/list; the [original candidate20 snapshot](mcp-tools.candidate-20.schema.json) and exact [prior16 snapshot](mcp-tools.daily-96a.schema.json) retain provenance. All16 old objects are unchanged.

Full records travel as record_json strings to preserve exact integers. After uncertain save, retain original_author_json and original Ref and explicitly GET to reconcile, without automatic write retry. The library is create-only; changed content needs a new ID. Daemon compilation is authoritative; adapter Record revision/typed-digest checks are not independent semantic recompilation. The 4MiB budget covers adapter-built CallToolResult only, excluding SDK-added _meta and JSON-RPC envelopes. The earlier1016-node/117-top-level software qualification (three explicit live skips) remains separate from installed1e2e765's actual stdio/authoring/task evidence and38 read-only installation checks. Software evidence alone never establishes physical support.

## 14. Complete HTTP operation index and errors

The following46 entries describe installed6ca:44 previous1e operations plus two conditional bilingual JavaScript resources. They cover execution, pure computation, storage, and static resources. `/v2` does not upgrade everything: light v2 means experimental Orbit, while Pattern v2 means source-preserving records and fixed-Ref execution. `{id}` and `{jobId}` in the same position are placeholder spellings, not separate routes.

OpenAPI has four additional explicit ordinary-ID GET aliases: `/v1/patterns/import`, `/v2/patterns/preview`, `/v2/patterns/import`, and `/v2/patterns/preview-ref`. Their GET semantics remain the corresponding `/patterns/{id}` read. They do not add independent capabilities.

The source6ca bundle host accepts three closed profiles: six public assets+LICENSE/two contracts; nine public assets+LICENSE/four contracts; or eleven public assets+LICENSE/the same four contracts. Manifest is counted separately and total dist stays at most256KiB. The bilingual profile adds both i18n.js and locales.js together. Older valid profiles return404 for them; malformed profiles or missing required bytes return500. GET /demo redirect is another canonical operation. Historical host 56c with old fe32507 bundle returned 404 for the three Pattern resources. Prior96a embedded Web f90ef8d/208132 bytes. Current1e2e765 embeds English Web cf5219 with10 manifest.files entries and210608 total bytes including manifest; its nine public resources were hash-checked in the native read-only window and after installation. A valid basic bundle still returns 404 for them; missing required files or digest/profile/lock mismatch returns 500. The English bundle changes content without changing routes. See [localized Web debugger details](web-debugger.zh-CN.md).

All routes share loopback Host/same-origin Origin protection and no-store/nosniff headers. Static pages do not create Jobs on load; explicit later actions call the APIs. Sources and tests below are repository links; their existence does not itself establish physical support. Installed state is6ca5a14; per-route historical availability is retained separately in the structured inventory.

<!-- canonical-http-operations:start -->

### GET /healthz

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** none. **Query:** none in documented canonical use.

**Success:** 200; {status:"ok",api_version:"v1"}.

**I/O and execution:** No service/USB call; proves HTTP handler only.

**Receipt:** none

**Error groups/codes:** global.

**Source:** [internal/httpapi/httpapi.go](../internal/httpapi/httpapi.go). **Tests:** [httpapi_test.go](../internal/httpapi/httpapi_test.go).

### GET /v1/capabilities

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** none. **Query:** RawQuery must be empty.

**Success:** 200; control.CapabilityCatalog; compile_contract, supported_inputs, effects, policies, color_model, led_selectors, program/effect limits, pattern_library, hardware_pattern_upload.

**I/O and execution:** No USB; if v1 Store configured, calls Store.List to report pattern_library, so not zero filesystem I/O.

**Receipt:** none

**Error groups/codes:** global, invalid_request.

**Bounds:** Old catalog describes Program/Effect and optionally v1 pattern_ref; use frame-capabilities separately. hardware_pattern_upload=false.

**Source:** [internal/httpapi/preview.go](../internal/httpapi/preview.go). **Tests:** [preview_test.go](../internal/httpapi/preview_test.go), [patterns_test.go](../internal/httpapi/patterns_test.go).

### GET /v1/instance

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** none. **Query:** none in documented canonical use.

**Success:** 200; InstanceInfo{daemon_instance_id,idempotency{supported,request_id_prefix,accept_until_uptime_ms,max_accept_window_ms,receipt_retention_ms,max_entries,max_retained_steps}}.

**I/O and execution:** No USB; computes uptime window; does not reserve receipt capacity or submit Job.

**Receipt:** Returns preparation metadata; caller appends 16 random bytes as 32 lowerhex chars to request_id_prefix.

**Error groups/codes:** global, idempotency_unsupported.

**Success headers:** X-NB1-Daemon-Instance-ID.

**Source:** [internal/httpapi/httpapi.go](../internal/httpapi/httpapi.go). **Tests:** [receipt_test.go](../internal/httpapi/receipt_test.go).

### GET /v1/transport-metrics

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** none. **Query:** none in documented canonical use.

**Success:** 200; []TransportMetric{serial,operation,calls,attempts,retries,failures,last_error?}.

**I/O and execution:** Only copied in-memory state/counters; handler does not request a USB sample. Background worker monitoring is separate.

**Receipt:** none

**Error groups/codes:** global.

**Bounds:** Missing optional Metrics provider returns []. LastError is historical; retries may have recovered.

**Source:** [internal/httpapi/httpapi.go](../internal/httpapi/httpapi.go). **Tests:** [httpapi_test.go](../internal/httpapi/httpapi_test.go).

### GET /v1/devices

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** none. **Query:** none in documented canonical use.

**Success:** 200; []DeviceState (empty is []).

**I/O and execution:** Only copied in-memory state/counters; handler does not request a USB sample. Background worker monitoring is separate.

**Receipt:** none

**Error groups/codes:** global.

**Source:** [internal/httpapi/httpapi.go](../internal/httpapi/httpapi.go). **Tests:** [httpapi_test.go](../internal/httpapi/httpapi_test.go).

### GET /v1/devices/{serial}

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** none. **Query:** none in documented canonical use.

**Success:** 200; DeviceState; serial/product/path?/firmware?/capabilities,connection,active_job?,queue_depth,desired?,observed?,last_error?.

**I/O and execution:** Only copied in-memory state/counters; handler does not request a USB sample. Background worker monitoring is separate.

**Receipt:** none

**Error groups/codes:** global, service.

**Bounds:** This is CLI control state route. No /state suffix; observed.sampled_at must be retained. desired is a target, not observation.

**Source:** [internal/httpapi/httpapi.go](../internal/httpapi/httpapi.go). **Tests:** [httpapi_test.go](../internal/httpapi/httpapi_test.go), [preview_test.go](../internal/httpapi/preview_test.go).

### POST /v1/programs/preview

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** Exactly one of program:Program / effect:Effect / pattern_ref:{id,revision}; optional serial. **Query:** RawQuery must be empty. **Raw body limit:** 1048576 bytes.

**Success:** 200; Program/Effect: PreviewResult{schema_version,compile_contract,input_kind,program,nominal_duration_ms,total_steps,required_capabilities,validation,device?}; reference: {pattern_ref,preview}.

**I/O and execution:** Raw Program/Effect pure; optional serial reads one cached snapshot only. pattern_ref reads/resolves v1 Store (filesystem I/O), never submits/opens USB.

**Receipt:** No policy/ticket/dry_run allowed. Does not prepare receipt.

**Error groups/codes:** global, json_1m, service, library.

**Source:** [internal/httpapi/preview.go](../internal/httpapi/preview.go). **Tests:** [preview_test.go](../internal/httpapi/preview_test.go), [patterns_test.go](../internal/httpapi/patterns_test.go).

### POST /v1/devices/{serial}/jobs

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** Exactly one program:Program / effect:Effect / pattern_ref:{id,revision}, policy; request_id and expected_daemon_instance_id both present or both absent for raw Program/Effect. **Query:** Canonical use has none; legacy raw branch does not explicitly reject all query/body extras beyond strict JSON schema. **Raw body limit:** 1048576 bytes.

**Success:** 202; Job (bare, not receipt wrapper).

**I/O and execution:** Queues same device worker; actual execution calls Stop(hold), Set/waits, final Stop(off|hold)+Read.

**Receipt:** Raw Program/Effect supports legacy unkeyed mode; supplied pair uses atomic receipt. v1 pattern_ref requires explicit reject + original ticket.

**Error groups/codes:** global, json_1m, service, library.

**Success headers:** keyed: X-NB1-Daemon-Instance-ID; keyed: X-NB1-Replayed true|false; Location: keyed original request URI; unkeyed /v1/jobs/{id}.

**Bounds:** Raw policy empty defaults replace; accepts replace/queue/reject. New callers should explicitly choose policy and ticket. CLI control set is a one-Step Program with on_finish:hold sent here; there is no separate /set HTTP route. v1 Pattern returns frozen Program Job and does not retain v2 provenance.

**Source:** [internal/httpapi/httpapi.go](../internal/httpapi/httpapi.go). **Tests:** [receipt_test.go](../internal/httpapi/receipt_test.go), [effects_test.go](../internal/httpapi/effects_test.go), [patterns_execution_test.go](../internal/httpapi/patterns_execution_test.go).

### GET /v1/devices/{serial}/requests/{request_id}

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** documented none. **Query:** Exactly one nonempty expected_daemon_instance_id.

**Success:** 200; Original current Job snapshot.

**I/O and execution:** Only copied in-memory state/counters; handler does not request a USB sample. Background worker monitoring is separate. Lookup can prune expired terminal receipts.

**Receipt:** Original serial+request_id+instance; no Store lookup.

**Error groups/codes:** global, service.

**Success headers:** X-NB1-Daemon-Instance-ID.

**Bounds:** 404 request_not_found does not prove original POST never arrived.

**Source:** [internal/httpapi/httpapi.go](../internal/httpapi/httpapi.go). **Tests:** [receipt_test.go](../internal/httpapi/receipt_test.go), [patterns_execution_test.go](../internal/httpapi/patterns_execution_test.go).

### GET /v1/jobs/{id}

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** none. **Query:** none in documented canonical use.

**Success:** 200; Job.

**I/O and execution:** Only copied in-memory state/counters; handler does not request a USB sample. Background worker monitoring is separate.

**Receipt:** ID only; bounded ordinary history, separate from receipt retention.

**Error groups/codes:** global, service.

**Source:** [internal/httpapi/httpapi.go](../internal/httpapi/httpapi.go). **Tests:** [httpapi_test.go](../internal/httpapi/httpapi_test.go), [receipt_test.go](../internal/httpapi/receipt_test.go).

### POST /v1/jobs/{id}/cancel

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** empty or {} only; no source/instance/ticket fields. **Query:** canonical none; old branch does not explicitly reject all query parameters.

**Success:** 202; Current Job, may be cancelling/queued-cancelled/already-terminal.

**I/O and execution:** Mutates cancellation state; worker confirms cleanup before active task terminal. Does not itself interrupt native I/O.

**Receipt:** ID only; no original instance precondition on this legacy route.

**Error groups/codes:** global, json_1m, service.

**Bounds:** Cancelling is not cleanup complete; cancelling succeeded hold returns terminal job without extinguishing held output.

**Source:** [internal/httpapi/httpapi.go](../internal/httpapi/httpapi.go). **Tests:** [httpapi_test.go](../internal/httpapi/httpapi_test.go), [engine_test.go](../internal/control/engine_test.go).

### POST /v1/colors/resolve

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** {color:ColorInput}. **Query:** none, including empty ?. **Raw body limit:** 4096 bytes.

**Success:** 200; {schema_version: 1,color_contract:"nb1-color-input/v1",input_kind,canonical_color,rgb 8:{r,g,b},palette?}.

**I/O and execution:** Pure bounded computation; no device discovery/open/Set/Read, no Job/receipt preparation.

**Receipt:** none

**Error groups/codes:** global, 405 method_not_allowed, 415 json_required, 413 body_too_large, 400 invalid_request(query), 400 invalid_color.

**Bounds:** No accepted:false or Job field in HTTP response; MCP wrapper is separate.

**Source:** [internal/httpapi/color.go](../internal/httpapi/color.go). **Tests:** [color_test.go](../internal/httpapi/color_test.go).

### GET /v1/frame-capabilities

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** none. **Query:** none including empty ?.

**Success:** 200; schema_version: 1,contract,experimental:true,software_supported,max_frames,max_executed_frames,max_target_writes,min/max_interval_ms,fade_quantum_ms,write_spacing_ms,max_nominal_duration_ms,policy:"reject",initialization,timing_basis,required_device_capabilities.

**I/O and execution:** No USB, type-asserts FrameService only.

**Receipt:** none

**Error groups/codes:** global, invalid_request.

**Source:** [internal/httpapi/frames.go](../internal/httpapi/frames.go). **Tests:** [frames_test.go](../internal/httpapi/frames_test.go).

### POST /v1/frame-programs/preview

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** {program:FrameProgram,serial?:string}. **Query:** none including empty ?. **Raw body limit:** 1048576 bytes.

**Success:** 200; FramePreview + device?:{serial,connection}.

**I/O and execution:** Pure schedule preview; serial checks only configured cached snapshot. No FrameService still returns valid preview with execution unavailable.

**Receipt:** none

**Error groups/codes:** global, json_1m, service.

**Bounds:** execution unavailable reason distinguishes not_selected/frame_service_unsupported/device_not_configured/device_not_online/missing_capabilities; no fallback to steps.

**Source:** [internal/httpapi/frames.go](../internal/httpapi/frames.go). **Tests:** [frames_test.go](../internal/httpapi/frames_test.go).

### POST /v1/devices/{serial}/frame-jobs

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** {program:FrameProgram,policy:"reject",request_id,expected_daemon_instance_id}. **Query:** none including empty ?. **Raw body limit:** 1048576 bytes.

**Success:** 202; FrameJob.

**I/O and execution:** Same worker/lease; paired target writes 10ms apart, host intervals, final fade wait and shared cleanup. No periodic Read during frame schedule.

**Receipt:** Mandatory complete original pair; no unkeyed/replace/queue raw Frame route.

**Error groups/codes:** global, json_1m, service, 501 frame_unsupported.

**Success headers:** X-NB1-Replayed; X-NB1-Daemon-Instance-ID; Location: /v1/devices/{serial}/frame-requests/{request_id}?expected_daemon_instance_id=....

**Source:** [internal/httpapi/frames.go](../internal/httpapi/frames.go). **Tests:** [frames_test.go](../internal/httpapi/frames_test.go), [frames_engine_test.go](../internal/httpapi/frames_engine_test.go).

### GET /v1/devices/{serial}/frame-requests/{request_id}

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** none. **Query:** Exactly one nonempty expected_daemon_instance_id.

**Success:** 200; Original FrameJob.

**I/O and execution:** Only copied in-memory state/counters; handler does not request a USB sample. Background worker monitoring is separate. No Store.

**Receipt:** Original frame receipt; Program receipt route does not substitute.

**Error groups/codes:** global, service, 501 frame_unsupported.

**Success headers:** X-NB1-Daemon-Instance-ID.

**Source:** [internal/httpapi/frames.go](../internal/httpapi/frames.go). **Tests:** [frames_test.go](../internal/httpapi/frames_test.go).

### GET /v1/frame-jobs/{id}

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** none. **Query:** none including empty ?.

**Success:** 200; FrameJob.

**I/O and execution:** Only copied in-memory state/counters; handler does not request a USB sample. Background worker monitoring is separate.

**Receipt:** ID only history.

**Error groups/codes:** global, service, 501 frame_unsupported.

**Source:** [internal/httpapi/frames.go](../internal/httpapi/frames.go). **Tests:** [frames_test.go](../internal/httpapi/frames_test.go).

### POST /v1/frame-jobs/{id}/cancel

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** empty / {} legacy; optional nonempty expected_daemon_instance_id binds instance; explicit null/blank/wrong type/unknown/duplicate fields rejected. **Query:** none including empty ?.

**Success:** 202; Current FrameJob; cancelling is possible.

**I/O and execution:** Checks supplied immutable instance before CancelFrame; only same worker performs Stop/Read.

**Receipt:** ID only legacy remains; optional instance is not full Action/ticket binding.

**Error groups/codes:** global, json_1m, service, 501 frame_unsupported.

**Success headers:** X-NB1-Daemon-Instance-ID only when conditional instance branch succeeds.

**Bounds:** CLI high v2 cancel first validates original Action+receipt, then uses this conditional cancel once; no dedicated HTTP action-cancel route. Current Web/MCP generic cancel uses existing ID-only branch; do not claim equal cross-instance protection.

**Source:** [internal/httpapi/frames.go](../internal/httpapi/frames.go). **Tests:** [frame_cancel_bound_test.go](../internal/httpapi/frame_cancel_bound_test.go), [frames_test.go](../internal/httpapi/frames_test.go).

### POST /v1/light/preview

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** {contract:"nb1-light-action/v1",action:{kind:set/off/fade/blink/breathe/pulse/alternate/rainbow,...},serial?:string}. **Query:** none including empty ?. **Raw body limit:** 65536 bytes.

**Success:** 200; {result: normalized Action + frozen Program/FrameProgram and compile facts, preview: matching low preview}.

**I/O and execution:** No Job/ticket/USB. Optional serial checks one cached device snapshot only.

**Receipt:** No policy/dry_run/ticket accepted.

**Error groups/codes:** global, json_64k, 400 invalid_light_action, service.

**Source:** [internal/httpapi/light.go](../internal/httpapi/light.go). **Tests:** [light_test.go](../internal/httpapi/light_test.go).

### POST /v1/devices/{serial}/light

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** {contract:"nb1-light-action/v1",action:{...},policy:"reject",request_id,expected_daemon_instance_id}. **Query:** none including empty ?. **Raw body limit:** 65536 bytes.

**Success:** 202; Job (bare).

**I/O and execution:** Original high intent compiled by nb1; same low worker admission/executor.

**Receipt:** Mandatory original pair; explicit reject only; no automatic POST replay in provided high clients.

**Error groups/codes:** global, json_64k, 400 invalid_light_action, service, 501 idempotency_unsupported.

**Success headers:** X-NB1-Replayed; X-NB1-Daemon-Instance-ID; Location: corresponding original Program/frame receipt.

**Bounds:** v1 old alternate.transition=orbit remains old sequential Program, not experimental-v2 orbit.

**Source:** [internal/httpapi/light.go](../internal/httpapi/light.go). **Tests:** [light_test.go](../internal/httpapi/light_test.go).

### POST /v2/light/preview

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** {contract:"nb1-light-action/experimental-v2",action:{kind:orbit only,...},serial?:string}. **Query:** none including empty ?. **Raw body limit:** 65536 bytes.

**Success:** 200; {result: normalized Action + frozen Program/FrameProgram and compile facts, preview: matching low preview}.

**I/O and execution:** No Job/ticket/USB. Optional serial checks one cached device snapshot only.

**Receipt:** No policy/dry_run/ticket accepted.

**Error groups/codes:** global, json_64k, 400 invalid_light_action, service.

**Source:** [internal/httpapi/light_v2.go](../internal/httpapi/light_v2.go). **Tests:** [frames_test.go](../internal/httpapi/frames_test.go), [frames_engine_test.go](../internal/httpapi/frames_engine_test.go).

### POST /v2/devices/{serial}/light

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** {contract:"nb1-light-action/experimental-v2",action:{...},policy:"reject",request_id,expected_daemon_instance_id}. **Query:** none including empty ?. **Raw body limit:** 65536 bytes.

**Success:** 202; FrameJob (bare).

**I/O and execution:** Original high intent compiled by nb1; same low worker admission/executor.

**Receipt:** Mandatory original pair; explicit reject only; no automatic POST replay in provided high clients.

**Error groups/codes:** global, json_64k, 400 invalid_light_action, service, 501 frame_unsupported.

**Success headers:** X-NB1-Replayed; X-NB1-Daemon-Instance-ID; Location: corresponding original Program/frame receipt.

**Bounds:** 4/8 segments; preserves high Action authoring; no hand-authored frames required.

**Source:** [internal/httpapi/light_v2.go](../internal/httpapi/light_v2.go). **Tests:** [frames_test.go](../internal/httpapi/frames_test.go), [frames_engine_test.go](../internal/httpapi/frames_engine_test.go).

### GET /v1/patterns

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** none. **Query:** none or exactly format=snapshot.

**Success:** 200; PatternList{schema_version: 1,contract,library,patterns}; format=snapshot canonical v1 Snapshot bytes.

**I/O and execution:** Filesystem Store read only; no device/service method, Job or execution ticket.

**Receipt:** none

**Error groups/codes:** global, library.

**Bounds:** configured v1 library selection only; does not expose v2 records.

**Source:** [internal/httpapi/patterns.go](../internal/httpapi/patterns.go). **Tests:** [patterns_test.go](../internal/httpapi/patterns_test.go).

### GET /v1/patterns/{id}

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** none. **Query:** none or exactly format=snapshot; IDs ASCII1–64.

**Success:** 200; Frozen v1 Record; format=snapshot single canonical Snapshot.

**I/O and execution:** Filesystem Store read only; no device/service method, Job or execution ticket.

**Receipt:** none

**Error groups/codes:** global, library.

**Bounds:** GET import is a legal ID.

**Source:** [internal/httpapi/patterns.go](../internal/httpapi/patterns.go). **Tests:** [patterns_test.go](../internal/httpapi/patterns_test.go).

### POST /v1/patterns/import

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** v1 author package or frozen snapshot accepted by Store.Import; not advanced Action source. **Query:** none including empty ?. **Raw body limit:** 1048576 bytes.

**Success:** 200; ImportResult{records,created,existing,total}.

**I/O and execution:** Filesystem create-only atomic import; no device/service method or execution.

**Receipt:** No execution receipt. publication_unknown is storage uncertainty, not Job uncertainty.

**Error groups/codes:** global, 415 json_required, 413 pattern_package_too_large, library.

**Source:** [internal/httpapi/patterns.go](../internal/httpapi/patterns.go). **Tests:** [patterns_test.go](../internal/httpapi/patterns_test.go).

### GET /v2/patterns

**Availability:** Integrated in installed6ca5a14 and previous1e; absent from historical 1d89.

**Input:** none. **Query:** none or exactly format=snapshot.

**Success:** 200; v2 List envelope {schema_version: 2,contract,library,pattern_execution,patterns}; format=snapshot canonical v2 Snapshot bytes.

**I/O and execution:** Filesystem Store read only; no device/service method, Job or execution ticket.

**Receipt:** none

**Error groups/codes:** global, library, library_v2.

**Bounds:** pattern_execution is wiring only; true pattern_service_configured; false service_unsupported/library_unconfigured/library_configuration_error with pattern_ prefixes; zero Store call to report status.

**Source:** [internal/httpapi/patterns_v2.go](../internal/httpapi/patterns_v2.go). **Tests:** [patterns_v2_test.go](../internal/httpapi/patterns_v2_test.go).

### GET /v2/patterns/{id}

**Availability:** Integrated in installed6ca5a14 and previous1e; absent from historical 1d89.

**Input:** none. **Query:** none or exactly format=snapshot; IDs ASCII1–64.

**Success:** 200; v2 {schema_version: 2,contract,record,verification,execution_digest}; format=snapshot single canonical Snapshot.

**I/O and execution:** Filesystem Store read only; no device/service method, Job or execution ticket.

**Receipt:** none

**Error groups/codes:** global, library, library_v2.

**Bounds:** GET preview/import/preview-ref/export/capabilities are ordinary IDs, not operation routes. Unknown source/recipe is explicit unverified read-only data, not executable. pattern_execution is wiring only; true pattern_service_configured; false service_unsupported/library_unconfigured/library_configuration_error with pattern_ prefixes; zero Store call to report status.

**Source:** [internal/httpapi/patterns_v2.go](../internal/httpapi/patterns_v2.go). **Tests:** [patterns_v2_test.go](../internal/httpapi/patterns_v2_test.go).

### POST /v2/patterns/import

**Availability:** Integrated in installed6ca5a14 and previous1e; absent from historical 1d89.

**Input:** Full verified canonical-compatible v2 frozen Snapshot; no authoring body. **Query:** none including empty ?. **Raw body limit:** 1048576 bytes.

**Success:** 200; {schema_version: 2,contract,records,created,existing,total}.

**I/O and execution:** Filesystem create-only atomic import; no device/service method or execution.

**Receipt:** No execution receipt. publication_unknown is storage uncertainty, not Job uncertainty.

**Error groups/codes:** global, 415 json_required, 413 pattern_package_too_large, library, library_v2.

**Bounds:** pattern_execution is wiring only; true pattern_service_configured; false service_unsupported/library_unconfigured/library_configuration_error with pattern_ prefixes; zero Store call to report status.

**Source:** [internal/httpapi/patterns_v2.go](../internal/httpapi/patterns_v2.go). **Tests:** [patterns_v2_test.go](../internal/httpapi/patterns_v2_test.go).

### POST /v2/patterns/preview

**Availability:** Integrated in installed6ca5a14 and previous1e; absent from historical 1d89.

**Input:** {id,title,source:{kind:"light_action",contract,action}}; no serial/policy/ticket/expected_revision/program. **Query:** none including empty ?. **Raw body limit:** 1048576 bytes.

**Success:** 200; {schema_version: 2,contract,record,verification,execution_digest,preview,pattern_execution:{available,reason}}.

**I/O and execution:** Pure codec only; Store zero calls including Path; Service zero calls; no USB.

**Receipt:** none

**Error groups/codes:** global, 415 json_required, 413 pattern_package_too_large, library_v2.

**Bounds:** pattern_execution is wiring only; true pattern_service_configured; false service_unsupported/library_unconfigured/library_configuration_error with pattern_ prefixes; zero Store call to report status.

**Source:** [internal/httpapi/patterns_v2.go](../internal/httpapi/patterns_v2.go). **Tests:** [patterns_v2_test.go](../internal/httpapi/patterns_v2_test.go).

### POST /v2/patterns

**Availability:** Integrated in installed6ca5a14 and previous1e; absent from historical 1d89.

**Input:** Same author source + expected_revision matching full freshly built candidate; create-only. **Query:** none including empty ?. **Raw body limit:** 1048576 bytes.

**Success:** [201, 200]; {schema_version: 2,contract,records,created,existing,total}; 201 new ID, 200 identical existing.

**I/O and execution:** Rebuild/verify pure candidate, then exactly one Store.Import; no execution/service/USB.

**Receipt:** No daemon/request ticket; expected_revision confirms candidate, not overwrite permission.

**Error groups/codes:** global, 415 json_required, 413 pattern_package_too_large, library, library_v2.

**Success headers:** Location: /v2/patterns/{id}.

**Bounds:** pattern_execution is wiring only; true pattern_service_configured; false service_unsupported/library_unconfigured/library_configuration_error with pattern_ prefixes; zero Store call to report status.

**Source:** [internal/httpapi/patterns_v2.go](../internal/httpapi/patterns_v2.go). **Tests:** [patterns_v2_test.go](../internal/httpapi/patterns_v2_test.go).

### POST /v2/patterns/preview-ref

**Availability:** Integrated in installed6ca5a14 and previous1e; absent from historical 1d89.

**Input:** {pattern_ref,serial?}. **Query:** none including empty ?. **Raw body limit:** 4096 bytes.

**Success:** 200; PatternRefPreviewV2.

**I/O and execution:** P5 exact Resolve and verification, pure Program/Frame facts; serial optionally one cached Device; no Job/receipt/USB.

**Receipt:** none

**Error groups/codes:** global, pattern_execution_v2, library_v2.

**Bounds:** Resolve once; no Path/other Store call. Strict raw POST4096B, valid UTF8, all fields closed ASCII; duplicate/unknown/null/ForceQuery rejected.

**Source:** [internal/httpapi/pattern_execution_v2.go](../internal/httpapi/pattern_execution_v2.go). **Tests:** [pattern_execution_v2_test.go](../internal/httpapi/pattern_execution_v2_test.go), [pattern_execution_v2_engine_test.go](../internal/httpapi/pattern_execution_v2_engine_test.go).

### POST /v2/devices/{serial}/pattern-jobs

**Availability:** Integrated in installed6ca5a14 and previous1e; absent from historical 1d89.

**Input:** {pattern_ref,policy,request_id,expected_daemon_instance_id}. **Query:** none including empty ?. **Raw body limit:** 4096 bytes.

**Success:** 202; PatternJobEnvelopeV2.

**I/O and execution:** Core new-receipt lazy Resolve then source-bound admission; accepted replay skips Store/config; worker uses existing execution.

**Receipt:** Original serial/request_id/instance + full Ref + explicit replace/queue/reject.

**Error groups/codes:** global, pattern_execution_v2.

**Success headers:** X-NB1-Daemon-Instance-ID: exactly one original instance; X-NB1-Replayed: exactly one true|false; Location: original Pattern receipt path + four URL encoded query fields.

**Bounds:** No Store precheck; no client Program/Action/digest. Strict raw POST4096B, valid UTF8, all fields closed ASCII; duplicate/unknown/null/ForceQuery rejected.

**Source:** [internal/httpapi/pattern_execution_v2.go](../internal/httpapi/pattern_execution_v2.go). **Tests:** [pattern_execution_v2_test.go](../internal/httpapi/pattern_execution_v2_test.go), [pattern_execution_v2_engine_test.go](../internal/httpapi/pattern_execution_v2_engine_test.go).

### GET /v2/devices/{serial}/pattern-requests/{request_id}

**Availability:** Integrated in installed6ca5a14 and previous1e; absent from historical 1d89.

**Input:** none. **Query:** exactly expected_daemon_instance_id,record_contract,pattern_id,revision, once each nonempty.

**Success:** 200; PatternJobEnvelopeV2.

**I/O and execution:** Core original receipt lookup only; zero Store/config/USB.

**Receipt:** Original path request_id + four exact scalar query fields.

**Error groups/codes:** global, pattern_execution_v2.

**Success headers:** X-NB1-Daemon-Instance-ID: exactly one original instance.

**Bounds:** No history/legacy Job fallback; retained terminal receipts remain queryable after Engine close. GET has no request body. Exactly four unique nonempty query fields are required: expected_daemon_instance_id, record_contract, pattern_id, revision; identity scalars are closed ASCII, and unknown, repeated, empty, or malformed query parts are rejected.

**Source:** [internal/httpapi/pattern_execution_v2.go](../internal/httpapi/pattern_execution_v2.go). **Tests:** [pattern_execution_v2_test.go](../internal/httpapi/pattern_execution_v2_test.go), [pattern_execution_v2_engine_test.go](../internal/httpapi/pattern_execution_v2_engine_test.go).

### POST /v2/devices/{serial}/pattern-requests/{request_id}/cancel

**Availability:** Integrated in installed6ca5a14 and previous1e; absent from historical 1d89.

**Input:** {pattern_ref,expected_daemon_instance_id,confirmed_job_id?}. **Query:** none including empty ?. **Raw body limit:** 4096 bytes.

**Success:** 202; PatternJobEnvelopeV2.

**I/O and execution:** Core bound cancel; zero Store/config lookup. Active cleanup is existing worker behavior.

**Receipt:** Original request_id/path, instance, full Ref; optional confirmed Job ID.

**Error groups/codes:** global, pattern_execution_v2.

**Success headers:** X-NB1-Daemon-Instance-ID: exactly one original instance.

**Bounds:** 202 may still cancelling/terminal; terminal hold not turned off; no implicit GET. Strict raw POST4096B, valid UTF8, all fields closed ASCII; duplicate/unknown/null/ForceQuery rejected.

**Source:** [internal/httpapi/pattern_execution_v2.go](../internal/httpapi/pattern_execution_v2.go). **Tests:** [pattern_execution_v2_test.go](../internal/httpapi/pattern_execution_v2_test.go), [pattern_execution_v2_engine_test.go](../internal/httpapi/pattern_execution_v2_engine_test.go).

### GET /demo

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** none. **Query:** none including empty ?.

**Success:** 307; 307 redirect to /demo/.

**I/O and execution:** Read embedded static bytes only; page load itself creates no light Job; subsequent explicit user actions call documented APIs.

**Receipt:** none

**Error groups/codes:** global, invalid_request, 404 not_found, 500 internal_error.

**Bounds:** Host56c supports complete basic/extended profiles; six or nine public assets plus LICENSE, manifest separate; fixed dist determines UI content, not frontend source directory.

**Source:** [internal/httpapi/light.go](../internal/httpapi/light.go). **Tests:** [light_test.go](../internal/httpapi/light_test.go).

### GET /demo/

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** none. **Query:** none including empty ?.

**Success:** 200; HTML index.

**I/O and execution:** Read embedded static bytes only; page load itself creates no light Job; subsequent explicit user actions call documented APIs.

**Receipt:** none

**Error groups/codes:** global, invalid_request, 404 not_found, 500 internal_error.

**Success headers:** Content-Security-Policy: default-src none; script/style/connect self; base/frame/form none; Referrer-Policy: no-referrer.

**Bounds:** Host56c supports complete basic/extended profiles; six or nine public assets plus LICENSE, manifest separate; fixed dist determines UI content, not frontend source directory.

**Source:** [internal/httpapi/light.go](../internal/httpapi/light.go). **Tests:** [light_test.go](../internal/httpapi/light_test.go).

### GET /demo/demo.js

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** none. **Query:** none including empty ?.

**Success:** 200; JS.

**I/O and execution:** Read embedded static bytes only; page load itself creates no light Job; subsequent explicit user actions call documented APIs.

**Receipt:** none

**Error groups/codes:** global, invalid_request, 404 not_found, 500 internal_error.

**Success headers:** Content-Security-Policy: default-src none; script/style/connect self; base/frame/form none; Referrer-Policy: no-referrer.

**Bounds:** Host56c supports complete basic/extended profiles; six or nine public assets plus LICENSE, manifest separate; fixed dist determines UI content, not frontend source directory.

**Source:** [internal/httpapi/light.go](../internal/httpapi/light.go). **Tests:** [light_test.go](../internal/httpapi/light_test.go).

### GET /demo/demo.css

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** none. **Query:** none including empty ?.

**Success:** 200; CSS.

**I/O and execution:** Read embedded static bytes only; page load itself creates no light Job; subsequent explicit user actions call documented APIs.

**Receipt:** none

**Error groups/codes:** global, invalid_request, 404 not_found, 500 internal_error.

**Success headers:** Content-Security-Policy: default-src none; script/style/connect self; base/frame/form none; Referrer-Policy: no-referrer.

**Bounds:** Host56c supports complete basic/extended profiles; six or nine public assets plus LICENSE, manifest separate; fixed dist determines UI content, not frontend source directory.

**Source:** [internal/httpapi/light.go](../internal/httpapi/light.go). **Tests:** [light_test.go](../internal/httpapi/light_test.go).

### GET /demo/orbit.html

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** none. **Query:** none including empty ?.

**Success:** 200; HTML Orbit experimental.

**I/O and execution:** Read embedded static bytes only; page load itself creates no light Job; subsequent explicit user actions call documented APIs.

**Receipt:** none

**Error groups/codes:** global, invalid_request, 404 not_found, 500 internal_error.

**Success headers:** Content-Security-Policy: default-src none; script/style/connect self; base/frame/form none; Referrer-Policy: no-referrer.

**Bounds:** Host56c supports complete basic/extended profiles; six or nine public assets plus LICENSE, manifest separate; fixed dist determines UI content, not frontend source directory.

**Source:** [internal/httpapi/light.go](../internal/httpapi/light.go). **Tests:** [light_test.go](../internal/httpapi/light_test.go).

### GET /demo/orbit.js

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** none. **Query:** none including empty ?.

**Success:** 200; JS.

**I/O and execution:** Read embedded static bytes only; page load itself creates no light Job; subsequent explicit user actions call documented APIs.

**Receipt:** none

**Error groups/codes:** global, invalid_request, 404 not_found, 500 internal_error.

**Success headers:** Content-Security-Policy: default-src none; script/style/connect self; base/frame/form none; Referrer-Policy: no-referrer.

**Bounds:** Host56c supports complete basic/extended profiles; six or nine public assets plus LICENSE, manifest separate; fixed dist determines UI content, not frontend source directory.

**Source:** [internal/httpapi/light.go](../internal/httpapi/light.go). **Tests:** [light_test.go](../internal/httpapi/light_test.go).

### GET /demo/orbit.css

**Availability:** Installed6ca5a14; previously1e2e765; also implemented in historical 1d89.

**Input:** none. **Query:** none including empty ?.

**Success:** 200; CSS.

**I/O and execution:** Read embedded static bytes only; page load itself creates no light Job; subsequent explicit user actions call documented APIs.

**Receipt:** none

**Error groups/codes:** global, invalid_request, 404 not_found, 500 internal_error.

**Success headers:** Content-Security-Policy: default-src none; script/style/connect self; base/frame/form none; Referrer-Policy: no-referrer.

**Bounds:** Host56c supports complete basic/extended profiles; six or nine public assets plus LICENSE, manifest separate; fixed dist determines UI content, not frontend source directory.

**Source:** [internal/httpapi/light.go](../internal/httpapi/light.go). **Tests:** [light_test.go](../internal/httpapi/light_test.go).

### GET /demo/patterns.html

**Availability:** Installed6ca bilingual Pattern bundle and previous1e extended bundle serve200; valid basic/old bundle serves 404. Historical1d89 lacked this route.

**Input:** none. **Query:** none including empty ?.

**Success:** [200, 404]; HTML if included, standard Error if valid legacy bundle omits asset.

**I/O and execution:** Read verified embedded bytes only; no Store/Job/device/USB. A Web resource route is not a hardware control API.

**Receipt:** none

**Error groups/codes:** global, 400 invalid_request, 404 not_found (valid legacy bundle), 500 internal_error (invalid bundle/required asset failure).

**Success headers:** Content-Security-Policy: default-src none; script/style/connect self; base/frame/form none; Referrer-Policy: no-referrer.

**Bounds:** Only complete extended profile adds all three Pattern assets and both v2 Pattern contracts; total dist including manifest <=256KiB. Host support does not import new dist or install daemon; old complete bundle remains valid and returns 404; required files/hash mismatch still 500.

**Source:** [internal/httpapi/light.go](../internal/httpapi/light.go). **Tests:** [light_test.go](../internal/httpapi/light_test.go), [embed_test.go](../internal/webui/embed_test.go).

### GET /demo/patterns.js

**Availability:** Installed6ca bilingual Pattern bundle and previous1e extended bundle serve200; valid basic/old bundle serves 404. Historical1d89 lacked this route.

**Input:** none. **Query:** none including empty ?.

**Success:** [200, 404]; JavaScript if included, standard Error if valid legacy bundle omits asset.

**I/O and execution:** Read verified embedded bytes only; no Store/Job/device/USB. A Web resource route is not a hardware control API.

**Receipt:** none

**Error groups/codes:** global, 400 invalid_request, 404 not_found (valid legacy bundle), 500 internal_error (invalid bundle/required asset failure).

**Success headers:** Content-Security-Policy: default-src none; script/style/connect self; base/frame/form none; Referrer-Policy: no-referrer.

**Bounds:** Only complete extended profile adds all three Pattern assets and both v2 Pattern contracts; total dist including manifest <=256KiB. Host support does not import new dist or install daemon; old complete bundle remains valid and returns 404; required files/hash mismatch still 500.

**Source:** [internal/httpapi/light.go](../internal/httpapi/light.go). **Tests:** [light_test.go](../internal/httpapi/light_test.go), [embed_test.go](../internal/webui/embed_test.go).

### GET /demo/patterns.css

**Availability:** Installed6ca bilingual Pattern bundle and previous1e extended bundle serve200; valid basic/old bundle serves 404. Historical1d89 lacked this route.

**Input:** none. **Query:** none including empty ?.

**Success:** [200, 404]; CSS if included, standard Error if valid legacy bundle omits asset.

**I/O and execution:** Read verified embedded bytes only; no Store/Job/device/USB. A Web resource route is not a hardware control API.

**Receipt:** none

**Error groups/codes:** global, 400 invalid_request, 404 not_found (valid legacy bundle), 500 internal_error (invalid bundle/required asset failure).

**Success headers:** Content-Security-Policy: default-src none; script/style/connect self; base/frame/form none; Referrer-Policy: no-referrer.

**Bounds:** Only complete extended profile adds all three Pattern assets and both v2 Pattern contracts; total dist including manifest <=256KiB. Host support does not import new dist or install daemon; old complete bundle remains valid and returns 404; required files/hash mismatch still 500.

**Source:** [internal/httpapi/light.go](../internal/httpapi/light.go). **Tests:** [light_test.go](../internal/httpapi/light_test.go), [embed_test.go](../internal/webui/embed_test.go).


### GET /demo/i18n.js

**Availability:** Installed6ca, conditional on the complete bilingual Pattern profile. Historical1e and valid older profiles return404.

**Input:** no query/body, including an empty `?`; GET only. **Success:**200 local JavaScript, `text/javascript; charset=utf-8`; no-store/nosniff, same-origin CSP and no-referrer. Invalid required bundle bytes/profile/lock return500.

**I/O / execution:** verified embedded bytes only; no filesystem Pattern access, Job, preparation, USB or permission request. Language selection changes UI text only, preserving raw payloads, user values and original tickets.

Source: [light.go](../internal/httpapi/light.go), [embed.go](../internal/webui/embed.go); tests: [light_test.go](../internal/httpapi/light_test.go), [embed_test.go](../internal/webui/embed_test.go).

### GET /demo/locales.js

**Availability:** Installed6ca, conditional on the complete bilingual Pattern profile. Historical1e and valid older profiles return404.

**Input:** no query/body, including an empty `?`; GET only. **Success:**200 local JavaScript, `text/javascript; charset=utf-8`; no-store/nosniff, same-origin CSP and no-referrer. Invalid required bundle bytes/profile/lock return500.

**I/O / execution:** verified embedded bytes only; no filesystem Pattern access, Job, preparation, USB or permission request. Language selection changes UI text only, preserving raw payloads, user values and original tickets.

Source: [light.go](../internal/httpapi/light.go), [embed.go](../internal/webui/embed.go); tests: [light_test.go](../internal/httpapi/light_test.go), [embed_test.go](../internal/webui/embed_test.go).

<!-- canonical-http-operations:end -->

### Error envelope and groups

The common shape is `{"error":{"code":"...","message":"..."}}`. message can vary by OS and is not a stable machine field. Route entries name these groups:

<!-- http-error-groups:start -->

#### global

| HTTP | Code/meaning |
| --- | --- |
| 403 | local_only: loopback Host and same-origin Origin required |
| 405 | method_not_allowed + Allow header |
| 404 | not_found: unknown route/resource |

#### json_1m

| HTTP | Code/meaning |
| --- | --- |
| 415 | json_required |
| 413 | body_too_large (1MiB), Connection close |
| 400 | invalid_json: strict object/unknown/duplicate/null/tail/type errors |

#### json_64k

| HTTP | Code/meaning |
| --- | --- |
| 415 | json_required |
| 413 | body_too_large (65536B), Connection close |
| 400 | invalid_json: strict envelope; invalid_light_action from authoritative action parser |

#### service

| HTTP | Code/meaning |
| --- | --- |
| 400 | invalid_request |
| 404 | not_found or request_not_found |
| 409 | idempotency_conflict / daemon_instance_changed / queue_full / busy |
| 410 | request_expired |
| 429 | idempotency_capacity |
| 503 | unavailable (offline/closed) |
| 500 | internal_error |
| 501 | idempotency_unsupported or frame_unsupported at optional service boundary |

#### library

| HTTP | Code/meaning |
| --- | --- |
| 400 | invalid_pattern_request |
| 404 | pattern_not_found |
| 409 | pattern_library_limit / pattern_revision_conflict / pattern_conflict / pattern_library_busy |
| 501 | pattern_library_unsupported |
| 503 | pattern_library_unavailable / pattern_commit_uncertain |

#### library_v2

| HTTP | Code/meaning |
| --- | --- |
| 400 | invalid_pattern_request |
| 409 | pattern_revision_conflict / pattern_library_read_only |
| 501 | pattern_source_unsupported or unconfigured pattern_library_unsupported |
| 503 | pattern_library_format_mismatch / pattern_commit_uncertain; uncertain response has no partial result/Location |

#### pattern_execution_v2

| HTTP | Code/meaning |
| --- | --- |
| 400 | invalid_pattern_request; core capability ErrInvalid remains invalid_request |
| 404 | pattern_not_found/request_not_found/not_found |
| 409 | idempotency_conflict/daemon_instance_changed/queue_full/busy/pattern_revision_conflict/pattern_library_busy |
| 410 | request_expired; missing expired key does not prove never executed |
| 429 | idempotency_capacity |
| 413 | body_too_large4096B +Connection close |
| 415 | json_required |
| 500 | pattern_integrity_error/internal_error |
| 501 | pattern_execution_unsupported/pattern_library_unsupported/pattern_source_unsupported |
| 503 | unavailable/pattern_library_unavailable/pattern_library_format_mismatch; preserve original ticket on uncertainty |

<!-- http-error-groups:end -->

### Compatibility and evidence bounds

Strict JSON behavior differs across generations. New Light/Frame/Color/Pattern routes reject queries including empty `?` where ForceQuery is checked; some older root/Job routes do not uniformly reject query/body extras. Prefer canonical documented use; an inventory does not implement absent guards. GET state is cached; preserve sampled_at, connection, desired/observed distinctions. Accepted task, terminal result, readback, and optical observation remain distinct.

New Pattern save reconstructs the authored candidate, checks expected_revision, then performs one create-only Import; a mismatch is 409 with zero Store calls. Integrity-valid unknown records remain readable/exportable, not newly importable/executable. An unknown library record makes writes read_only. Publication uncertainty is reconciled read-only using original ID/full Record; no automatic retry. A new submission failure must not replace old active work; an accepted receipt survives later Store/configuration failure under the core rules.

The source6ca inventory has46 canonical operations including five conditional assets. OpenAPI's50 operations include four aliases; historical1e had44 operations. Documentation inventory is source-derived; frozen historical Recorder observations are not relabeled as new HTTP evidence; JSON Schema does not prove raw lexical rules, source hashes, device support, or visual quality. [Structured operation inventory](api-http-inventory.json) retains historical provenance. [Low-level inputs](api-low-examples.json) are data, not executed requests.

## 15. Design boundaries and future work

High level preserves intent while Low level preserves precise targets; one color core, separate preview/save/run operations, shared device workers, source revisions, and original receipts keep these roles explicit. Keep artistic meanings distinct: blink is on/off, breathe symmetric intensity, pulse attack/decay/rest, alternate exchange, rainbow hue cycle, orbit paired phase/intensity flow.

Policy differences, pulse's extra 30ms per cycle, and hold's final-output behavior are current contract facts. Future changes need an explicit version. Generic compose/sequence, new names, user palettes, synchronization, automatic recovery, MCP snapshot import/export, and device RAM/Flash/notes interfaces are not implemented public APIs. CLI snapshot import/export exists in installed6ca; it does not create hardware-storage APIs. Distribution, Web/HID direct access, hooks, scripting, and a formal website remain separate work. Local planning pages are internal review artifacts.

Software tests and documentation checks use fixed local sources, pure compilation/Recorder examples, temporary libraries, memory drivers, and isolated fixtures as applicable; they do not upgrade physical qualifications. This documentation pass does not access hardware, daemon, browser, or private camera material. The separate baseline physical/installation windows retain their own evidence and limits.

## 16. Models, example files, and references

[OpenAPI](openapi.json) is the complete structural source for the following 111 models. Request/default semantics, conditional relationships, exact lexical rules, hash domains, and identity binding also require the prose and authoritative codec. A schema match alone does not prove admission or execution.

<!-- schema-models:start -->

| Model | Structural outline (follow the model link for all fields/bounds) |
| --- | --- |
| [`StepRequest`](openapi.json#/components/schemas/StepRequest) | Object. Required: `color` |
| [`Step`](openapi.json#/components/schemas/Step) | Object. Required: `led`, `color`, `fade_ms`, `hold_ms` |
| [`ProgramRequest`](openapi.json#/components/schemas/ProgramRequest) | Object. Required: `steps` |
| [`Program`](openapi.json#/components/schemas/Program) | Object. Required: `steps`, `repeat`, `on_finish` |
| [`SubmitRequest`](openapi.json#/components/schemas/SubmitRequest) | Object; see schema for conditional required fields. |
| [`DeviceState`](openapi.json#/components/schemas/DeviceState) | Object. Required: `serial`, `product`, `capabilities`, `connection`, `queue_depth` |
| [`Observation`](openapi.json#/components/schemas/Observation) | Object. Required: `led1`, `led2`, `sampled_at` |
| [`Job`](openapi.json#/components/schemas/Job) | Object. Required: `id`, `device`, `status`, `program`, `completed_steps`, `total_steps`, `created_at` |
| [`Error`](openapi.json#/components/schemas/Error) | Object. Required: `error` |
| [`Health`](openapi.json#/components/schemas/Health) | Object. Required: `status`, `api_version` |
| [`EmptyObject`](openapi.json#/components/schemas/EmptyObject) | Constrained object. |
| [`TransportMetric`](openapi.json#/components/schemas/TransportMetric) | Object. Required: `serial`, `operation`, `calls`, `attempts`, `retries`, `failures` |
| [`InstanceInfo`](openapi.json#/components/schemas/InstanceInfo) | Object. Required: `daemon_instance_id`, `idempotency` |
| [`IdempotencyInfo`](openapi.json#/components/schemas/IdempotencyInfo) | Object. Required: `supported`, `request_id_prefix`, `accept_until_uptime_ms`, `max_accept_window_ms`, `receipt_retention_ms`, `max_entries`, `max_retained_steps` |
| [`RequestTicket`](openapi.json#/components/schemas/RequestTicket) | Object. Required: `request_id`, `expected_daemon_instance_id` |
| [`SteadyEffect`](openapi.json#/components/schemas/SteadyEffect) | Object. Required: `kind`, `color` |
| [`PulseEffect`](openapi.json#/components/schemas/PulseEffect) | Object. Required: `kind`, `color` |
| [`Effect`](openapi.json#/components/schemas/Effect) | Alternatives: SteadyEffect, PulseEffect |
| [`PreviewRequest`](openapi.json#/components/schemas/PreviewRequest) | Object; see schema for conditional required fields. |
| [`PreviewValidation`](openapi.json#/components/schemas/PreviewValidation) | Object. Required: `program`, `device` |
| [`PreviewDevice`](openapi.json#/components/schemas/PreviewDevice) | Object. Required: `serial`, `capabilities` |
| [`PreviewResult`](openapi.json#/components/schemas/PreviewResult) | Object. Required: `schema_version`, `compile_contract`, `input_kind`, `program`, `nominal_duration_ms`, `total_steps`, `required_capabilities`, `validation` |
| [`ProgramLimits`](openapi.json#/components/schemas/ProgramLimits) | Object. Required: `max_steps`, `max_repeat`, `max_fade_ms`, `fade_quantum_ms`, `max_hold_ms`, `max_nominal_duration_ms`, `step_overhead_ms` |
| [`EffectLimits`](openapi.json#/components/schemas/EffectLimits) | Object. Required: `min_pulse_period_ms`, `max_pulse_period_ms`, `max_pulse_cycles` |
| [`CapabilityCatalog`](openapi.json#/components/schemas/CapabilityCatalog) | Object. Required: `schema_version`, `api_version`, `compile_contract`, `supported_inputs`, `effects`, `policies`, `color_model`, `led_selectors`, `preview_path`, `pattern_library`, `hardware_pattern_upload`, `program_limits`, `effect_limits`, `max_request_bytes` |
| [`ColorRGB8`](openapi.json#/components/schemas/ColorRGB8) | Object. Required: `space`, `r`, `g`, `b` |
| [`ColorHSV`](openapi.json#/components/schemas/ColorHSV) | Object. Required: `space`, `h_deg`, `s_pct`, `v_pct` |
| [`ColorInput`](openapi.json#/components/schemas/ColorInput) | Alternatives: string, ColorRGB8, ColorHSV |
| [`ColorResolveRequest`](openapi.json#/components/schemas/ColorResolveRequest) | Object. Required: `color` |
| [`ResolvedRGB8`](openapi.json#/components/schemas/ResolvedRGB8) | Object. Required: `r`, `g`, `b` |
| [`ColorResolution`](openapi.json#/components/schemas/ColorResolution) | Object. Required: `schema_version`, `color_contract`, `input_kind`, `canonical_color`, `rgb8` |
| [`LightActionSet`](openapi.json#/components/schemas/LightActionSet) | Object. Required: `kind`, `color` |
| [`NormalizedLightActionSet`](openapi.json#/components/schemas/NormalizedLightActionSet) | Object. Required: `kind`, `on_finish`, `led`, `color` |
| [`LightActionOff`](openapi.json#/components/schemas/LightActionOff) | Object. Required: `kind` |
| [`NormalizedLightActionOff`](openapi.json#/components/schemas/NormalizedLightActionOff) | Object. Required: `kind`, `on_finish`, `led` |
| [`LightActionFade`](openapi.json#/components/schemas/LightActionFade) | Object. Required: `kind`, `color`, `duration_ms` |
| [`NormalizedLightActionFade`](openapi.json#/components/schemas/NormalizedLightActionFade) | Object. Required: `kind`, `on_finish`, `led`, `color`, `duration_ms` |
| [`LightActionBlink`](openapi.json#/components/schemas/LightActionBlink) | Object. Required: `kind`, `color` |
| [`NormalizedLightActionBlink`](openapi.json#/components/schemas/NormalizedLightActionBlink) | Object. Required: `kind`, `on_finish`, `led`, `color`, `cycles`, `on_ms`, `off_ms` |
| [`LightActionBreathe`](openapi.json#/components/schemas/LightActionBreathe) | Object. Required: `kind`, `color` |
| [`NormalizedLightActionBreathe`](openapi.json#/components/schemas/NormalizedLightActionBreathe) | Object. Required: `kind`, `on_finish`, `led`, `color`, `cycles`, `period_ms` |
| [`LightActionPulse`](openapi.json#/components/schemas/LightActionPulse) | Object. Required: `kind`, `color` |
| [`NormalizedLightActionPulse`](openapi.json#/components/schemas/NormalizedLightActionPulse) | Object. Required: `kind`, `on_finish`, `led`, `color`, `cycles`, `attack_ms`, `decay_ms`, `rest_ms` |
| [`LightActionAlternate`](openapi.json#/components/schemas/LightActionAlternate) | Object. Required: `kind` |
| [`NormalizedLightActionAlternate`](openapi.json#/components/schemas/NormalizedLightActionAlternate) | Object. Required: `kind`, `on_finish`, `cycles`, `colors`, `leds`, `period_ms` |
| [`LightActionRainbow`](openapi.json#/components/schemas/LightActionRainbow) | Object. Required: `kind` |
| [`NormalizedLightActionRainbow`](openapi.json#/components/schemas/NormalizedLightActionRainbow) | Object. Required: `kind`, `on_finish`, `led`, `cycles`, `period_ms` |
| [`LightAction`](openapi.json#/components/schemas/LightAction) | Alternatives: LightActionSet, LightActionOff, LightActionFade, LightActionBlink, LightActionBreathe, LightActionPulse, LightActionAlternate, LightActionRainbow |
| [`NormalizedLightAction`](openapi.json#/components/schemas/NormalizedLightAction) | Alternatives: NormalizedLightActionSet, NormalizedLightActionOff, NormalizedLightActionFade, NormalizedLightActionBlink, NormalizedLightActionBreathe, NormalizedLightActionPulse, NormalizedLightActionAlternate, NormalizedLightActionRainbow |
| [`LightRequest`](openapi.json#/components/schemas/LightRequest) | Object. Required: `contract`, `action` |
| [`LightPreviewRequest`](openapi.json#/components/schemas/LightPreviewRequest) | Object. Required: `contract`, `action` |
| [`LightSubmitRequest`](openapi.json#/components/schemas/LightSubmitRequest) | Object. Required: `contract`, `action`, `policy`, `request_id`, `expected_daemon_instance_id` |
| [`LightCompilation`](openapi.json#/components/schemas/LightCompilation) | Object. Required: `schema_version`, `contract`, `program_compile_contract`, `action`, `program`, `nominal_duration_ms`, `total_steps`, `required_capabilities` |
| [`LightPreviewResponse`](openapi.json#/components/schemas/LightPreviewResponse) | Object. Required: `result`, `preview` |
| [`PatternRef`](openapi.json#/components/schemas/PatternRef) | Object. Required: `id`, `revision` |
| [`PatternSource`](openapi.json#/components/schemas/PatternSource) | Object; see schema for conditional required fields. |
| [`PatternRecord`](openapi.json#/components/schemas/PatternRecord) | Object. Required: `id`, `title`, `source`, `compiled_program`, `compile_contract`, `revision` |
| [`PatternAuthoringRecord`](openapi.json#/components/schemas/PatternAuthoringRecord) | Object. Required: `id`, `title` |
| [`PatternAuthoringPackage`](openapi.json#/components/schemas/PatternAuthoringPackage) | Object. Required: `schema_version`, `patterns` |
| [`PatternSnapshot`](openapi.json#/components/schemas/PatternSnapshot) | Object. Required: `schema_version`, `kind`, `records` |
| [`PatternImportPackage`](openapi.json#/components/schemas/PatternImportPackage) | Alternatives: PatternAuthoringPackage, PatternSnapshot |
| [`PatternLibraryInfo`](openapi.json#/components/schemas/PatternLibraryInfo) | Object. Required: `path`, `state`, `create_only`, `max_records`, `max_package_bytes`, `record_contract`, `compile_contract`, `snapshot_kind` |
| [`PatternSummary`](openapi.json#/components/schemas/PatternSummary) | Object. Required: `id`, `title`, `revision`, `compile_contract`, `total_steps`, `nominal_duration_ms`, `on_finish`, `required_capabilities` |
| [`PatternList`](openapi.json#/components/schemas/PatternList) | Object. Required: `schema_version`, `contract`, `library`, `patterns` |
| [`PatternImportResult`](openapi.json#/components/schemas/PatternImportResult) | Object. Required: `records`, `created`, `existing`, `total` |
| [`PatternPreview`](openapi.json#/components/schemas/PatternPreview) | Object. Required: `pattern_ref`, `preview` |
| [`FrameTargetRequest`](openapi.json#/components/schemas/FrameTargetRequest) | Object. Required: `led`, `color` |
| [`FrameRequest`](openapi.json#/components/schemas/FrameRequest) | Object. Required: `targets`, `fade_ms`, `interval_ms` |
| [`FrameProgramRequest`](openapi.json#/components/schemas/FrameProgramRequest) | Object. Required: `contract`, `initial`, `frames` |
| [`FrameTarget`](openapi.json#/components/schemas/FrameTarget) | Object. Required: `led`, `color` |
| [`Frame`](openapi.json#/components/schemas/Frame) | Object. Required: `targets`, `fade_ms`, `interval_ms` |
| [`FrameProgram`](openapi.json#/components/schemas/FrameProgram) | Object. Required: `contract`, `initial`, `frames`, `repeat`, `on_finish` |
| [`FrameExecutionAvailability`](openapi.json#/components/schemas/FrameExecutionAvailability) | Object. Required: `available`, `reason` |
| [`FramePreview`](openapi.json#/components/schemas/FramePreview) | Object. Required: `contract`, `program`, `total_frames`, `stored_target_writes`, `planned_target_writes`, `cycle_duration_ms`, `nominal_initial_ms`, `nominal_tail_ms`, `nominal_duration_ms`, `timing_basis`, `required_capabilities`, `execution` |
| [`FramePreviewRequest`](openapi.json#/components/schemas/FramePreviewRequest) | Object. Required: `program` |
| [`FrameSubmitRequest`](openapi.json#/components/schemas/FrameSubmitRequest) | Object. Required: `program`, `policy`, `request_id`, `expected_daemon_instance_id` |
| [`FrameCancelRequest`](openapi.json#/components/schemas/FrameCancelRequest) | Object; see schema for conditional required fields. |
| [`FrameJob`](openapi.json#/components/schemas/FrameJob) | Object. Required: `id`, `device`, `status`, `program`, `completed_frames`, `total_frames`, `completed_target_writes`, `planned_target_writes`, `created_at` |
| [`FrameCapabilities`](openapi.json#/components/schemas/FrameCapabilities) | Object. Required: `schema_version`, `contract`, `experimental`, `software_supported`, `max_frames`, `max_executed_frames`, `max_target_writes`, `min_interval_ms`, `max_interval_ms`, `fade_quantum_ms`, `write_spacing_ms`, `max_nominal_duration_ms`, `policy`, `initialization`, `timing_basis`, `required_device_capabilities` |
| [`OrbitAction`](openapi.json#/components/schemas/OrbitAction) | Object. Required: `kind` |
| [`NormalizedOrbitAction`](openapi.json#/components/schemas/NormalizedOrbitAction) | Object. Required: `kind`, `colors`, `leds`, `period_ms`, `cycles`, `segments`, `on_finish` |
| [`OrbitCompilation`](openapi.json#/components/schemas/OrbitCompilation) | Object. Required: `schema_version`, `contract`, `program_compile_contract`, `recipe_id`, `action`, `program`, `nominal_duration_ms`, `total_frames`, `planned_target_writes`, `required_capabilities` |
| [`OrbitPreviewRequest`](openapi.json#/components/schemas/OrbitPreviewRequest) | Object. Required: `contract`, `action` |
| [`OrbitSubmitRequest`](openapi.json#/components/schemas/OrbitSubmitRequest) | Object. Required: `contract`, `action`, `policy`, `request_id`, `expected_daemon_instance_id` |
| [`OrbitPreviewResponse`](openapi.json#/components/schemas/OrbitPreviewResponse) | Object. Required: `result`, `preview` |
| [`PatternRefV2`](openapi.json#/components/schemas/PatternRefV2) | Object. Required: `record_contract`, `id`, `revision` |
| [`PatternOpaqueJSON`](openapi.json#/components/schemas/PatternOpaqueJSON) | Alternatives: string, boolean, number, array, object |
| [`PatternSourceV2Read`](openapi.json#/components/schemas/PatternSourceV2Read) | Object. Required: `kind`, `contract`, `action` |
| [`PatternSourceV2Known`](openapi.json#/components/schemas/PatternSourceV2Known) | Alternatives: object, object |
| [`PatternExecutionV2`](openapi.json#/components/schemas/PatternExecutionV2) | Alternatives: object, object |
| [`PatternCompiledV2Read`](openapi.json#/components/schemas/PatternCompiledV2Read) | Object. Required: `normalized_action`, `execution` |
| [`PatternRecordV2Read`](openapi.json#/components/schemas/PatternRecordV2Read) | Object. Required: `record_contract`, `id`, `title`, `source`, `compiled`, `revision` |
| [`PatternRecordV2Known`](openapi.json#/components/schemas/PatternRecordV2Known) | Alternatives: object, object |
| [`PatternSnapshotV2Read`](openapi.json#/components/schemas/PatternSnapshotV2Read) | Object. Required: `schema_version`, `kind`, `records` |
| [`PatternSnapshotV2Write`](openapi.json#/components/schemas/PatternSnapshotV2Write) | Object. Required: `schema_version`, `kind`, `records` |
| [`PatternAuthorPreviewRequestV2`](openapi.json#/components/schemas/PatternAuthorPreviewRequestV2) | Object. Required: `id`, `title`, `source` |
| [`PatternAuthorSaveRequestV2`](openapi.json#/components/schemas/PatternAuthorSaveRequestV2) | Object. Required: `id`, `title`, `source`, `expected_revision` |
| [`PatternVerificationV2`](openapi.json#/components/schemas/PatternVerificationV2) | Object. Required: `verified`, `reason` |
| [`PatternExecutionAvailabilityV2`](openapi.json#/components/schemas/PatternExecutionAvailabilityV2) | Alternatives: object, object |
| [`PatternRecordResponseV2`](openapi.json#/components/schemas/PatternRecordResponseV2) | Object. Required: `schema_version`, `contract`, `record`, `verification`, `execution_digest` |
| [`PatternPreviewFactsV2`](openapi.json#/components/schemas/PatternPreviewFactsV2) | Alternatives: object, object |
| [`PatternPreviewResponseV2`](openapi.json#/components/schemas/PatternPreviewResponseV2) | Object. Required: `schema_version`, `contract`, `record`, `verification`, `execution_digest`, `preview`, `pattern_execution` |
| [`PatternSummaryV2`](openapi.json#/components/schemas/PatternSummaryV2) | Object. Required: `pattern_ref`, `title`, `source_contract`, `execution_kind`, `execution_contract`, `verification`, `execution_digest` |
| [`PatternLibraryInfoV2`](openapi.json#/components/schemas/PatternLibraryInfoV2) | Object. Required: `path`, `state`, `create_only`, `max_records`, `max_package_bytes`, `record_contract`, `snapshot_kind` |
| [`PatternListV2`](openapi.json#/components/schemas/PatternListV2) | Object. Required: `schema_version`, `contract`, `library`, `pattern_execution`, `patterns` |
| [`PatternWriteResponseV2`](openapi.json#/components/schemas/PatternWriteResponseV2) | Object. Required: `schema_version`, `contract`, `records`, `created`, `existing`, `total` |
| [`PatternRefPreviewRequestV2`](openapi.json#/components/schemas/PatternRefPreviewRequestV2) | Object. Required: `pattern_ref` |
| [`PatternSubmitRequestV2`](openapi.json#/components/schemas/PatternSubmitRequestV2) | Object. Required: `pattern_ref`, `policy`, `request_id`, `expected_daemon_instance_id` |
| [`PatternCancelRequestV2`](openapi.json#/components/schemas/PatternCancelRequestV2) | Object. Required: `pattern_ref`, `expected_daemon_instance_id` |
| [`PatternJobEnvelopeV2`](openapi.json#/components/schemas/PatternJobEnvelopeV2) | Alternatives: object, object |
| [`PatternRefPreviewV2`](openapi.json#/components/schemas/PatternRefPreviewV2) | Alternatives: object, object |

<!-- schema-models:end -->

The 14 example JSON files below are unchanged input data. Opening/saving them does not run a task. `.action.json` files feed light preview/run; Program/Effect/Frame and ColorInput have their own envelopes. pattern-v2.preview.json is a host author request supported by installed6ca and previous1e, absent only from historical1d89. Author title strings are user data and remain untouched. Real execution additionally needs a selected serial, explicit policy as required, and original ticket.

<!-- json-examples:start -->

| Example | Input meaning |
| --- | --- |
| [alternate.action.json](examples/api/alternate.action.json) | High-level color exchange |
| [blink.action.json](examples/api/blink.action.json) | High-level on/off blinking |
| [breathe.action.json](examples/api/breathe.action.json) | High-level symmetric breathing |
| [fade.action.json](examples/api/fade.action.json) | High-level target fade |
| [hsv.color-input.json](examples/api/hsv.color-input.json) | Bare HSV ColorInput for resolution |
| [legacy.effect.json](examples/api/legacy.effect.json) | Legacy symmetric-pulse Effect |
| [low.frame-program.json](examples/api/low.frame-program.json) | Experimental raw FrameProgram |
| [low.program.json](examples/api/low.program.json) | Exact sequential Program |
| [off.action.json](examples/api/off.action.json) | High-level off |
| [orbit.action.json](examples/api/orbit.action.json) | Experimental paired-fade Orbit |
| [pattern-v2.preview.json](examples/api/pattern-v2.preview.json) | Host Pattern v2 authored draft (Unicode title preserved) |
| [pulse.action.json](examples/api/pulse.action.json) | High-level attack/decay/rest pulse |
| [rainbow.action.json](examples/api/rainbow.action.json) | High-level hue cycle |
| [set.action.json](examples/api/set.action.json) | High-level set with ColorInput |

<!-- json-examples:end -->

- [English example guide](examples/api/README.md), with the [localized guide](examples/api/README.zh-CN.md) retained.
- [Installed MCP20](mcp-tools.schema.json), [prior MCP16](mcp-tools.daily-96a.schema.json), [historical MCP12](mcp-tools.daily-1d89.schema.json), and [original candidate20](mcp-tools.candidate-20.schema.json) retain separate provenance.
- [Agent Skill](../skills/blink1-control/SKILL.md), [English Pattern workflow](../skills/blink1-control/references/patterns.md), and [llms.txt](../llms.txt) guide use without granting additional permissions.
- [Localized agent guide](agent-guide.zh-CN.md) and [localized CLI guide](patterns-cli.md) retain historical detail; use current availability and actual discovery for installed status.
- [Device backend](../internal/devicebackend/README.md) describes the lower SDK boundary. This reference does not replace the full pinned SDK signatures or invent HTTP USB/Flash commands.
