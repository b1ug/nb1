# API v1 implementation

This development candidate provides the Action → Sequence → Job execution path through HTTP and `nb1 control`. The running local installation has its own separately tested source version.

The [OpenAPI 3.1 contract](api-v1.openapi.json) describes the current HTTP operations, request and response schemas, constraints and software-fixture examples. The valid saved-color name `resolve` has a concrete PUT operation alongside POST color resolution.

## User configuration

On macOS and Linux, the profile is `$XDG_CONFIG_HOME/nb1/config.yaml`. An empty or unset `XDG_CONFIG_HOME` selects `~/.config/nb1/config.yaml`. A nonempty XDG root must be absolute; relative values report an error. Windows keeps its native user configuration location, normally `%APPDATA%\nb1\config.yaml`, and ignores XDG for this selection. Select a file with `--config`, then `NB1_CONFIG`, then the default profile path, in that order. When the optional default profile is absent, the existing `setting.yaml` lookup remains available. An explicitly selected missing or invalid file reports an error.

```sh
nb1 config path
nb1 config init
nb1 config show
```

`config init` targets the selected profile path, creates a private template and refuses to overwrite an existing file. Before that profile exists, `config path` and `config show` may report the existing `setting.yaml` fallback. Its empty `device` and `server.devices` fields leave device selection to the user. These commands do not enumerate USB or start a daemon. `config show` reports settings resolved from the selected file, defaults and environment, including `NB1_DEVICE`, `NB1_PORT` and `NB1_LIBRARY_DIR`. It does not inspect or reload the running daemon.

```yaml
device: ""
server:
  devices: []
  listen: "127.0.0.1:8931"
jobs:
  terminal_limit: 16384
  terminal_ttl: 30m
library_dir: ""
```

`device` is an empty string or a quoted eight-hexadecimal-digit serial. `server.devices` is an explicit list of such strings. `jobs.terminal_limit` is a positive integer and `jobs.terminal_ttl` is a positive duration, such as `30m` or `1h`. These are defaults that users may change. An empty `library_dir` selects `library` under the same application root as the default profile, including the selected XDG root on macOS/Linux. A custom library path must be absolute.

Selecting a different configuration file keeps the application's default storage base stable. Device ownership keeps its established native OS policy, `os.UserConfigDir()/nb1/device-locks`. On macOS this is `~/Library/Application Support/nb1/device-locks`, separate from the XDG profile and library. Retaining this coordination directory lets the candidate share the default device lease with the older 6ca controller. An explicit lock-directory override must be shared by every controller accessing that device. Profile and library files remain in their existing locations until the user explicitly chooses another location; this path policy performs no file migration.

## Actions and sequences

An Action describes intent; a Sequence specifies exact targets and timing. Both use the same execution core. Their defaults differ: `set`, `off` and `fade` always hold their final output, while a raw Sequence defaults to `on_finish: "off"`.

### Action fields

`kind` is required and accepts exactly the nine lowercase names below. Each row lists all action-specific fields. Fields belonging to another Action are rejected, even when their value is zero. For example, `off` accepts no `color`, and `set` accepts neither `cycles` nor `on_finish`.

| Field | Required / omitted behavior | Accepted values |
| --- | --- | --- |
| `kind` | Required. | `set`, `off`, `fade`, `blink`, `breathe`, `pulse`, `alternate`, `orbit`, `rainbow`. |
| `led` | Optional, defaults to `0`; available on every Action except `alternate` and `orbit`. | Integer `0` for all outputs, `1` or `2` for a physical channel. |
| `color` | Required on `set`, `fade`, `blink`, `breathe` and `pulse`. | One ColorInput, described below. |
| `colors` | Required on `alternate` and `orbit`. | Exactly two ColorInputs; there is no default color pair. |
| `leds` | Optional on `alternate` and `orbit`, defaults to `[1,2]`. | Exactly `[1,2]` or `[2,1]`; order assigns the two colors. |
| `cycles` | Optional on the six finite repeating effects, defaults to `1`. | Positive integer, further limited by the compiled Step count and 24-hour budget. No unlimited repeat. |
| `on_finish` | Optional on those repeating effects, defaults to `"off"`. | `"off"` or `"hold"`; `""` is invalid. Not accepted on `set`, `off` or `fade`, which always hold. |

All Action durations are integer milliseconds. Use nonnegative integer JSON notation, without strings, signs, decimal points or exponents. Fade durations use 10ms units; a period or hold value need not be a multiple of 10 unless stated.

| Action | Required beyond `kind` | Timing fields: omission default; inclusive range | Compiled Steps per cycle / maximum `cycles` |
| --- | --- | --- | --- |
| `set` | `color` | No timing fields. One immediate update, then hold. | 1 / not accepted |
| `off` | None | No timing fields. Sets the selected output to black, then holds. This does not cancel another Job. | 1 / not accepted |
| `fade` | `color`, `duration_ms` | `duration_ms`: required, 10–600,000, multiple of 10. Starts from the current device color, then holds the target. | 1 / not accepted |
| `blink` | `color` | `on_ms`: 250; 10–3,600,010. `off_ms`: 250; same range. These include each update's 10ms allowance. | 2 / 512 |
| `breathe` | `color` | `period_ms`: 2,000; 330–9,600,314. A symmetric envelope, currently 16 fades plus the initial black Step. | 17 / 60 |
| `pulse` | `color` | `attack_ms`: 60; 10–600,000, multiple of 10. `decay_ms`: 240; same range and unit. `rest_ms`: 700; 0–3,600,000. | 3 / 341 |
| `alternate` | `colors` | `period_ms`: 2,000; 40–7,200,040. Two colors exchange assignments through four ordered immediate writes. | 4 / 256 |
| `orbit` | `colors` | `period_ms`: 3,000; 200–2,400,000. Four paired fades with shifted brightness; uses interval timing. | 4 / 256 |
| `rainbow` | None | `period_ms`: 3,000; 150–4,200,143. Seven fixed anchors, returning to red. | 8 / 128 |

The per-row cycle maximum is an additional ceiling, not a promise that every duration fits. Every compiled Action must also fit 1,024 executed Steps and 86,400,000ms nominal duration. The seven Rainbow anchors are `#ff0000`, `#ffa500`, `#ffff00`, `#00ff00`, `#0000ff`, `#4b0082` and `#8f00ff`; the eighth Step returns to the first color. Preview the compiled plan to inspect rounded fade times and update allowances.

### ColorInput

ColorInput is an authoring value used in Action `color` / `colors` and color resolution or saving. It has no omitted-color default.

| Form | Example | Rules |
| --- | --- | --- |
| Named color | `"rebeccapurple"` | One of 148 frozen opaque CSS names. ASCII case-insensitive; surrounding ASCII whitespace is trimmed. `transparent` and `currentColor` are not supported. |
| Hex | `"#369"`, `"#336699"` | Exactly three or six hex digits after `#`, case-insensitive. Surrounding ASCII whitespace is trimmed. Alpha and bare hex without `#` are not accepted. |
| RGB8 | `{"space":"rgb8","r":32,"g":96,"b":128}` | Exactly these four fields, all required. Channels are integers 0–255. |
| HSV / HSB | `{"space":"hsv","h_deg":210,"s_pct":75,"v_pct":50}` | Exactly these four fields, all required. `h_deg`: 0–360 inclusive, with 360 equivalent to 0. `s_pct` and `v_pct`: 0–100 inclusive. HSB uses this same `hsv` wire form; `space:"hsb"` is not accepted. |
| Local alias | `"@status-ok"` | Exact lowercase name matching `[a-z][a-z0-9-]{0,31}` after `@`; no surrounding whitespace. Requires a readable local library and an existing saved color. |

RGB8 channels reject decimal points, signs and exponent notation. HSV channels accept ordinary nonnegative decimal numbers with at most six fractional digits, without signs or exponents. HSV conversion rounds each final RGB8 channel to the nearest integer, with halves rounded upward. Objects do not default any channel. Arrays, CSS function strings such as `rgb(...)`, HSL and alpha are not part of this grammar.

A ColorInput is limited to 4,096 encoded JSON bytes. An ordinary color string is limited to 64 decoded bytes before trimming. Resolution returns lowercase exact `#rrggbb`; it performs no extra host gamma correction or optical calibration. A missing alias is an invalid color (`400 invalid_request`), while an unavailable or unreadable library produces `503 unavailable`.

### Sequence and Step fields

Sequence JSON is the exact low-level host plan. Targets use exact Hex6 colors, not ColorInput names, aliases or objects.

| Sequence field | Required / omitted behavior | Accepted values |
| --- | --- | --- |
| `timing` | Required. | `"serial"` or `"interval"`. |
| `steps` | Required. | 1–1,024 Step objects in execution order; `[]` is invalid. |
| `initial` | Required for `interval`; forbidden for `serial`, including an empty array. | One or two Target objects with distinct physical `led` values `1` / `2`. Applied once before all repetitions. |
| `repeat` | Optional, defaults to `1`. | Positive integer with `len(steps) * repeat <= 1024`; explicit `0` is invalid. |
| `on_finish` | Optional, defaults to `"off"`. | `"off"` or `"hold"`; empty string is invalid. |

| Step / Target field | Required / omitted behavior | Accepted values |
| --- | --- | --- |
| Step `targets` | Required. | Serial: exactly one Target. Interval: same count and LED order as `initial`. |
| Step `fade_ms` | Optional, defaults to `0`. | Integer 0–600,000, multiple of 10. Zero selects an immediate update through the compatible fade command. |
| Step `hold_ms` | Serial only; optional, defaults to `0`. Forbidden in interval Steps, including `0`. | Integer 0–3,600,000. |
| Step `interval_ms` | Required for interval Steps; forbidden for serial Steps. | Integer 50–600,000, at least `fade_ms`. |
| Target `led` | Required, including when selecting all outputs. | Serial: `0`, `1` or `2`. Interval / initial: `1` or `2`, without duplicates. No string selector. |
| Target `color` | Required. | Exact `#RRGGBB` string, with no whitespace; uppercase is accepted and normalized to lowercase. |

All numeric fields above use integer JSON numbers, not quoted numbers, decimal or exponent notation. The entire plan must fit the 1,024 executed-Step and 24-hour nominal budgets. There is no unbounded or implicit infinite Sequence.

Serial nominal duration is `repeat * sum(fade_ms + hold_ms + 10)`. Interval nominal duration includes `10ms * len(initial)`, the repeated intervals and any final settling allowance. The default Pulse expands to 1,030ms; one default Orbit cycle has 3,000ms of intervals plus 40ms of initial/final allowances, totaling 3,040ms.

These are host scheduling budgets, not optical deadlines. Native I/O, timer lateness, observations and cleanup can extend wall time. Interval target writes are ordered with device-update spacing, not simultaneous; a late interval does not cause a burst of catch-up writes. Host Sequences do not upload a DeviceSequence or write device Flash.

Create an input file with one Action or Sequence, for example `{"action":{"kind":"pulse","color":"lime","cycles":2}}`. Preview and run it with the candidate CLI.

```sh
nb1 control --endpoint http://127.0.0.1:8931 preview action.json
nb1 control --endpoint http://127.0.0.1:8931 run action.json --wait
nb1 control --endpoint http://127.0.0.1:8931 state
nb1 control --endpoint http://127.0.0.1:8931 job j_7K3XJNY9ZM
nb1 control --endpoint http://127.0.0.1:8931 cancel j_7K3XJNY9ZM --wait
```

For `control run [SERIAL] FILE` and `control state [SERIAL]`, select the device from an explicit positional serial, root `--device`, `NB1_DEVICE`, or profile `device`, in that order. With none selected, the client reads the daemon's cached device list and uses it only when exactly one device is configured. Multiple devices require an explicit choice. This lookup does not enumerate USB, launch the daemon or switch to direct access.

`control preview FILE` leaves `serial` absent by default, including when the profile has a preferred device. Add `--serial SERIAL` explicitly for the selected device's cached capability check.

## Daemon selection and connection failures

By default, `control` calls the selected loopback daemon. It does not start a daemon or
silently fall back to USB. A failed connection gives commands for explicitly
starting `nb1 server --devices SERIAL --listen HOST:PORT`, starting a previously
installed user service with `nb1 service start`, or inspecting USB directly with
the separate basic command `nb1 --device SERIAL read state`.

The hint uses the requested device and endpoint when known. A lost submission
response still preserves its original Job ID and query command; it is not
treated as proof that the daemon never received the request. No automatic
resubmission occurs. Choose `control --direct run` explicitly for a new foreground
Action/Sequence run; it does not replay an uncertain daemon submission.

## HTTP operations

These operations are the current runtime's API v1 route table. Success bodies are the values described below, without a surrounding `result` wrapper. They use JSON with `Cache-Control: no-store`. Preview compiles and optionally checks cached capabilities; it never starts playback.

### Common request rules

- Body-bearing operations require `Content-Type: application/json`; media-type parameters such as `charset=utf-8` are accepted. The entire body, including whitespace, is limited to 1,048,576 bytes. Each embedded ColorInput has its smaller 4,096-byte limit.
- Send one valid Unicode JSON object. Unknown, duplicate or incorrectly cased keys, trailing values, excessive nesting beyond 64 levels and explicit `null` anywhere are rejected. Omit an optional field to select its documented default; `null` does not mean omission.
- Empty values are not general defaults. `id: ""` requests Job ID generation; `title: ""` clears a Pattern title. Empty `serial`, `pattern_id`, `color`, `timing` or `on_finish`, and empty required target/Step arrays, are invalid.
- Preview, Job, device and library routes reject query parameters, including a bare trailing `?`. Job lookup/cancel and device/library GET routes accept no body, including `{}` or a chunked body. `Content-Type` is unnecessary when there is no body. The shared `/healthz` and `/v1/transport-metrics` handlers currently check only the method and do not inspect body/query values; clients should send neither.
- Use a loopback Host. A browser `Origin`, when present, must match the request scheme and exact Host/port. An absent Origin is accepted for non-browser clients. A different browser origin, including `Origin: null`, is rejected; this API is not a cross-origin remote-control endpoint.

### Requests and success statuses

| Request | Required input | Optional input / omission default | Success |
| --- | --- | --- | --- |
| `POST /v1/preview` | Exactly one of `action`, `sequence`, `pattern_id`. | `serial`: omitted means compile without a device check. | `200`, Preview object. |
| `POST /v1/jobs` | `serial` and exactly one of `action`, `sequence`, `pattern_id`. | `id`: omitted or `""` generates an ID. | `202` for new acceptance; `200` for matching replay. Both return a Job and `Location: /v1/jobs/{id}`. |
| `GET /v1/jobs/{id}` | Job ID in the path; no body. | None. | `200`, Job object, including when that Job failed. |
| `POST /v1/jobs/{id}/cancel` | Job ID in the path; no body. | None. | `202` while cancellation remains in progress; `200` when already terminal or cancelled before execution. Returns the current Job. |
| `POST /v1/colors/resolve` | `color`: one ColorInput. | None. | `200`, `{"color":"#rrggbb"}`. Does not save or play. |
| `GET /v1/colors` | No body. | None. | `200`, array of Color records; empty library returns `[]`. |
| `PUT /v1/colors/{name}` | Color name in the path and `color`: one ColorInput in the body. | None. | `200`, saved Color record, for both create and replacement. |
| `GET /v1/patterns` | No body. | None. | `200`, array of `{id,title?}` summaries; no Sequence payloads. Empty library returns `[]`. |
| `GET /v1/patterns/{id}` | Pattern ID in the path; no body. | None. | `200`, complete Pattern record. |
| `PUT /v1/patterns/{id}` | Pattern ID in the path and exactly one of `action` or `sequence` in the body. | `title`: omitted or `""` means no title, replacing any previous title. | `200`, complete saved Pattern, for both create and replacement. Does not play. |
| `GET /v1/devices` | No body. | None. | `200`, configured device states, sorted by serial; `[]` when none are configured. Does not discover USB. |
| `GET /v1/devices/{serial}` | Configured serial in the path; no body. | None. | `200`, one cached Device object. An unmatched serial returns `404`. |
| `GET /healthz` | No fields. | None. | `200`, `{"status":"ok","api_version":"v1"}`. HTTP health does not imply device availability. |
| `GET /v1/transport-metrics` | No fields. | None. | `200`, TransportMetric array; `[]` when no counters are available. |

There are no body fields beyond those listed for each operation. For example, Pattern PUT accepts no body `id` or `pattern_id`; Color PUT accepts no body `name`. `PUT /v1/colors/resolve` saves a color whose valid name is `resolve`; `POST` at that same path resolves a color without saving.

Job IDs and Pattern IDs are case-sensitive strings of 1–64 ASCII letters, digits, hyphens or underscores. Saved color names are case-sensitive lowercase strings matching `[a-z][a-z0-9-]{0,31}`. Preview/submission `serial` is a required nonempty string when present, with 1–128 ASCII letters, digits, hyphens or underscores. It must exactly match a configured device to execute. This HTTP grammar is wider than the profile's eight-hex-digit device setting. The device GET performs a direct cached lookup; an unmatched path value returns `404`, rather than a separate serial-format error.

An execution input can refer to a locally saved Pattern with `{"pattern_id":"gentle-pulse"}`. That reference is required to exist when compiling a new input. Pattern PUT instead requires an Action or Sequence and freezes its resolved Sequence. A matching replay of a retained Job returns the original Job before reading the current library.

### Preview and Job responses

Preview always returns `sequence` (the normalized plan), `total_steps` (integer 1–1,024, including repeats), `nominal_duration_ms` (integer milliseconds within the 24-hour budget), `required_capabilities` (string array) and `device_check`:

| `device_check.status` | Meaning |
| --- | --- |
| `not_requested` | `serial` was omitted. No device was selected. |
| `unknown` | The configured device is not online or has no cached capabilities; `reason` explains this. |
| `unsupported` | Cached online capabilities do not cover the compiled plan; `reason` explains this. |
| `supported` | The cached profile covers the plan. Admission can still fail later because the device became busy or offline. |

An unconfigured serial produces `404`; it is not an `unknown` check result. An online device with a nonempty capability list that lacks a required capability produces a successful Preview with `unsupported`, while submitting that plan produces `422 unsupported_capability`. An empty capability list remains `unknown`.

Serial plans require `set`, `sequence` and `raw-rgb`; interval plans require `set`, `raw-rgb` and the current `mk2-basic` profile. Both add `hold` when `on_finish` is `"hold"`. The returned `required_capabilities` contains these public names; it is not an assurance of physical support for an untested device generation.

| Job field | Presence / meaning |
| --- | --- |
| `id`, `serial` | Always present. Identify this accepted execution and device. |
| `status` | Always present: `queued`, `running`, `cancelling`, `succeeded`, `cancelled` or `failed`. |
| `completed_steps`, `total_steps` | Always present integers, with `0 <= completed_steps <= total_steps <= 1024` and `total_steps >= 1`. Counts include repetitions, exclude initial interval targets and cleanup. Progress counts host Step/interval completion, not optical settling. |
| `created_at` | Always present, UTC timestamp. |
| `started_at` | Omitted until execution starts; can remain omitted for a queued Job cancelled before execution. |
| `finished_at` | Omitted until terminal. Its presence identifies a terminal Job. |
| `error` | Omitted when empty. On failure, a human-readable explanation; it is not a nested HTTP error object. |

Timestamps use RFC 3339 format and can contain fractional seconds. A `202` submission or cancel response acknowledges a state transition, not successful playback or darkness. Poll the original Job ID. A cancellation racing with completion can return the existing successful terminal Job. Repeating cancellation on a terminal Job returns its unchanged status and completion time.

For a Job that actually started, successful or cancelled terminal publication follows device cleanup and readback. Cancelling a still-queued Job removes it without executing its device writes; it does not perform a new cleanup/readback or turn off another Job. Cleanup or readback failures take precedence and can produce `failed` even after cancellation was requested. A failed Job does not guarantee the device is off.

### Device, library and metric responses

| Record | Fields and omission rules |
| --- | --- |
| Device | Always includes `serial`, `product`, `connection`, `capabilities`, `queue_depth` and `observed`. `product` can be empty; capabilities can be `[]`. `queue_depth` is a nonnegative integer, bounded by the runtime's queue limit, at most 1,024. `firmware` is an integer omitted when unknown. `active_job` is omitted when there is no active Job. `observed` is `null` until an observation is available, otherwise `{led1,led2,sampled_at}`. The two colors are raw device RGB values; the timestamp says when they were sampled. |
| Color | Always `{name,color}`; `color` is canonical lowercase Hex6. Saving an alias freezes its resolved value, not a live reference. |
| Pattern summary | Always `id`; `title` is omitted when empty. GET list returns summaries only. |
| Complete Pattern | Always `id` and normalized `sequence`; `title` is omitted when empty. An omitted title on PUT clears the previous title. |
| TransportMetric | Always `serial`, `operation`, `calls`, `attempts`, `retries`, `failures`. The counters are unsigned 64-bit JSON integers. `calls` counts logical SDK operations, `attempts` includes first attempts, `retries` counts additional attempts, and `failures` counts terminally failed operations. Optional `last_error` is historical and may describe an operation that later recovered. Counters accumulate for the current driver's lifetime. |

Device connection can be `connecting`, `online`, `offline`, `busy`, `error`, `closing` or `closed`. During interval playback the worker defers periodic RGB queries to preserve scheduling, so cached values may predate the Job. A cached RGB value or successful final readback does not measure physical light, smoothness or camera-visible color. Use optical observation separately.

### HTTP errors and uncertain results

An HTTP error is `{"error":{"code":"...","message":"..."}}`. Invalid Action compilation can also include `error.field`, such as `action.color` or `action.colors[1]`. That field is optional; Sequence and other validation errors can describe the location only in `message`. Use status and `code` for handling; human-readable messages are not stable identifiers.

| Status | `error.code` | Current cause / handling |
| --- | --- | --- |
| `400` | `invalid_request` | Invalid JSON syntax or shape, unknown/duplicate/null fields, invalid identifier/color/timing, body/query where forbidden, missing required input, or a read-only runtime rejecting execution. Missing color aliases also use this code. |
| `400` | `invalid_json` | An error while reading the HTTP request body. Ordinary JSON decoder/validation failures on these v1 body routes use `invalid_request`. |
| `403` | `local_only` | Host is not loopback or the browser Origin does not match. |
| `404` | `not_found` | Unknown route, configured device, retained Job or saved Pattern. Expired/evicted Jobs also return this code; absence does not prove they never ran. |
| `405` | `method_not_allowed` | Unsupported method. `Allow` lists the route's supported method(s); HEAD is not an implicit GET. |
| `409` | `job_conflict` | Retained ID is bound to a different device or JSON input. Query the original ID; do not invent a new ID to retry uncertain work. |
| `409` | `device_busy` | Device is owned/busy, an execution or inspection is active, or admission cannot enqueue it. These v1 submissions reject competing work; no replace/queue policy field is accepted. |
| `413` | `body_too_large` | HTTP body exceeds 1 MiB. A smaller ColorInput or compiled-Pattern limit violation instead uses `400 invalid_request`. |
| `415` | `json_required` | Missing, malformed or non-`application/json` Content-Type on a body-bearing operation. |
| `422` | `unsupported_capability` | Submission needs capabilities absent from the device's cached profile. |
| `429` | `library_capacity` | Library record count exceeds its limit, or adding a new color/Pattern would exceed capacity. Replacing an existing record at capacity is allowed. |
| `503` | `unavailable` | Runtime closed/offline, sequence execution unsupported by the service, request context ended, or local library unavailable/unreadable/unsafe/locked. |
| `500` | `internal_error` | Unexpected synchronous service failure. Record the response and original identifiers before deciding what to do next. |

HTTP errors concern request handling or admission. An accepted Job can later fail because of native I/O, scheduling or cleanup; GET still returns `200` with `status: "failed"` and Job `error`. Do not treat every `200` as playback success, or every transport failure as rejection.

If a submission times out, disconnects or loses its response, retain the original pre-generated ID and query it. Do not automatically POST again, allocate another ID, or switch to direct USB. A missing record after restart, expiry or eviction leaves the outcome unknown. Omitting `id` is convenient, but if that first response is lost the caller may not know the generated ID. There is no endpoint to list retained Jobs.

Library PUT also has an uncertain-result boundary: replacement may have completed before a later filesystem error or response loss. Read the Pattern by its original ID, or inspect the color list / resolve its `@name`, before deciding on another PUT. There is no dedicated commit-uncertainty code or revision precondition in this v1 library; another writer can replace the current value.

A request deadline bounds the caller's wait, not a synchronous native call already in progress. The worker retains its native ownership while it remains alive and the call is outstanding. Existing transport retries can repeat a fade write; Job-ID deduplication is admission control, not a guarantee of exactly-once physical updates. A process exit or native crash can break the connection outside this JSON error contract, and a restarted daemon has no previous in-memory Job records.

## CLI connection policy

The agreed default for `nb1 control` is to report that the daemon is unavailable and let the user explicitly choose to start a daemon or use direct access. It must not start a daemon or fall back to USB automatically. Starting `nb1 server` does not install a system service.

For an explicit foreground run without HTTP, use the same Action, Sequence or saved Pattern input:

```sh
nb1 control --direct run DEVICE_SERIAL action.json
nb1 control --direct run DEVICE_SERIAL action.json --timeout 2m
```

Direct mode is available only for `run`. It always waits for completion; no daemon or installed service is required. A serial must come from the argument, `--device`, `NB1_DEVICE` or profile `device`. Direct mode does not discover a default device. It validates the input and resolves local colors and Patterns before opening the device, then uses the same compiler and execution core as the daemon. The native ownership lock rejects a second controller while a daemon or another process owns that device.

Each invocation is a new playback with a process-local Job ID. `--id` and `--endpoint` are rejected in direct mode. The Job cannot be queried or deduplicated by a later command. Without an explicit `--timeout`, a valid finite Sequence runs to completion. An explicit timeout or Ctrl-C requests cancellation, followed by a separate bounded cleanup wait, defaulting to ten seconds. Successful `hold` completion releases the handle without adding an off command.

Output has `mode: "direct"`, the Job when created, and `engine_closed`. That boolean reports confirmed engine teardown, not playback success or an optical observation. Check the Job status and command result separately. If the cleanup wait expires, the command reports an unknown device state; a wait deadline does not cancel a native call. The engine does not proactively release its handle or device lease while native I/O is outstanding. Process termination and native crashes are separate limits, and bounded command exit does not prove device cleanup.

On macOS and Linux, install the current user's login service using the running executable:

```sh
nb1 service install --devices DEVICE_SERIAL --start
```

Omitting `--binary` selects the current process's executable through the operating system. It does not search PATH for another installation. An explicit `--binary /absolute/path/to/nb1` overrides that selection. The native manager validates the file and resolves symlinks before saving the configuration. Without `--start`, installation enables login loading without requesting immediate startup.

Service installation resolves devices and listen address from its flags and selected profile. It records the actual configuration file's absolute path when a file is selected, and stores device/listen values as explicit startup arguments. Those arguments keep the installed device selection and address stable when the profile is edited. macOS/Linux managed services also record the selected XDG root in their controlled startup environment, keeping the default library stable across login environments. Library and Job-cache settings are read from the profile on restart. Update the managed service configuration explicitly when changing its device list or listen address.

Cross-platform `service render` still requires an explicit target `--binary` path. Windows service installation remains unsupported. Automatic path selection does not implement service upgrade migration: package version paths, including Homebrew Cellar targets, still need upgrade and rollback qualification.

## Job IDs and deduplication

An omitted or empty `id` generates `j_` followed by ten random Crockford Base32 characters. Custom IDs accept 1–64 ASCII letters, digits, hyphens and underscores. The CLI generates its ID before submission so it can report that ID when a response is lost.

The daemon keeps each active Job. Terminal Jobs use a configurable soft TTL measured from terminal completion, defaulting to thirty minutes. LRU pressure or a daemon restart can remove records sooner. Job lookups, matching duplicate submissions and terminal cancellation queries update their LRU order while leaving the original expiration time intact. A duplicate ID with the same device and JSON input returns the existing Job with HTTP 200. Different input or device returns HTTP 409. Input identity ignores object-key ordering and preserves array order and number spelling. It compares author input, not just the compiled result: omitting a default versus supplying it, changing a color name's case or switching to an equivalent Hex value can therefore conflict even when the resulting light would be the same.

The terminal cache defaults to 16,384 Jobs. Set its positive capacity with profile `jobs.terminal_limit` or server `--job-cache-size`, and set its positive TTL with `jobs.terminal_ttl` or `--job-cache-ttl`. Explicit server flags override profile values. Restart the daemon to apply changes. When the selected limit is exceeded, the least recently used terminal record is evicted. Active Jobs stay pinned and release their execution plans when they become terminal. Device ownership and per-Sequence limits govern new execution. Cache capacity does not reject new Jobs.

Deduplication applies while the original record is retained. After expiration or eviction, submitting the same ID can start a new Job. The records belong to the current daemon process's memory. The default thirty-minute TTL is an upper retention bound, not a guaranteed deduplication window or exactly-once execution guarantee.

## Local colors and Patterns

The daemon selects its library from `--library-dir`, `NB1_LIBRARY_DIR`, profile `library_dir`, or the stable per-user `nb1/library` default, in that order. Colors and Patterns are already stored there. A custom `--config` path alone does not relocate the library. Reads return saved records or empty lists without creating a missing directory. An unconfigured or unreadable library does not prevent plain Sequence or non-alias Action compilation; operations that need the library return `503 unavailable`.

A write creates the directories and atomically replaces one complete record; it is not a partial update. Omitted `title` clears the old title. The library supports 256 named colors and 128 Patterns, with serialized records up to 1 MiB, including formatting, and titles up to 256 Unicode code points. Replacing an existing record remains possible at the count limit. Pattern IDs preserve case, including on case-insensitive filesystems. Lists fail as a whole if a record cannot be read; they do not return silent partial results. There are currently no library delete, revision or compare-and-swap operations.

Cooperating writers serialize saves. Lock contention waits up to about two seconds before `503 unavailable`; this does not bound all filesystem I/O. Concurrent successful saves can replace each other's values. The uncertain-write recovery rule above still applies after an error response.

```sh
nb1 control colors set status-ok lime
nb1 control colors list
nb1 control patterns save gentle-pulse action.json --title "Gentle pulse"
nb1 control patterns get gentle-pulse
nb1 control patterns list
```

Pattern saving fixes the resolved Sequence. Updating a color or Pattern affects new Jobs. Repeated submission of an accepted Job returns that Job before reading the library, including when its referenced record is updated or temporarily unavailable.

## Initial delivery scope

The agreed first delivery targets the currently verified Mac/MK2 setup: macOS 27.2 on arm64, blink(1) MK2 with firmware 204. Other operating systems, architectures and device generations must show their actual evidence level separately: software tests, compilation, physical USB, or not yet tested. A software CI result does not establish physical-device support.

This scope does not require every platform or MK3 hardware to be qualified before the first delivery. Existing recovered I/O retries remain a known limitation; confirming scope does not create a tag or publish a release. Work continues in order: hardware, daemon/CLI, Web and packaging, then the surrounding integrations.

## Implementation sequence

The source delivery includes the core execution path, local library, HTTP operations above, CLI commands, explicit direct run, embedded Web controls and MCP adapter. Software checks cover the integrated routes, direct lifecycle and Agent workflows. Finite daemon CLI/HTTP checks on the local macOS 27.2 / MK2 FW204 candidate cover serial and interval playback, Orbit, Job replay, busy rejection, cancellation, final RGB readback and ownership. Additional same-source MK2 checks cover direct set/hold, explicit timeout and SIGINT cleanup, real stdio MCP preview/submission/Job lookup after session closure, and Web startup/preview without unintended playback. Full Web playback acceptance, broader platform/device qualification, and replacement of the daily installation remain pending. DeviceSequence routes are planned. The [terminology table](api-v1-terminology.md) governs names across those surfaces.

macOS/Linux user-service installation pins `XDG_CONFIG_HOME` to the verified configuration root used for that installation. A different login-manager environment cannot redirect the selected profile or default library. Device locks continue to follow the native OS coordination policy described above. Rendering alone can set the target configuration root with `service render --user-config-root`; it does not grant filesystem access.
