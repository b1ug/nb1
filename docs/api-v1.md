# API v1 implementation

This development candidate provides the Action → Sequence → Job execution path through HTTP and `nb1 control`. The running local installation has its own separately tested source version.

The [OpenAPI 3.1 contract](api-v1.openapi.json) describes the current HTTP operations, request and response schemas, constraints and software-fixture examples. The valid saved-color name `resolve` has a concrete PUT operation alongside POST color resolution.

## Actions and sequences

Actions are `set`, `off`, `fade`, `blink`, `breathe`, `pulse`, `alternate`, `orbit` and `rainbow`. The compiler produces one normalized Sequence with `timing` set to `serial` or `interval` and an ordered `steps` array. Orbit uses four paired transitions. Rainbow uses seven color anchors and returns to red.

Colors accept names, short or full hex, RGB8, HSV and local references such as `@status-ok`. The compiled Sequence contains exact RGB values. Sequence input contains exact six-digit hex targets, integer millisecond durations and explicit timing. The core validates executed-step limits, time units, target order and total duration.

The preview includes scheduled device-update allowances in its nominal duration. The default pulse expands to 1,030 ms. One default orbit cycle has 3,000 ms of intervals plus 40 ms for initialization and final settling, totaling 3,040 ms. Actual runtime also includes native I/O and cleanup.

Create an input file with one Action or Sequence, for example `{"action":{"kind":"pulse","color":"lime","cycles":2}}`. Preview and run it with the candidate CLI.

```sh
nb1 control --endpoint http://127.0.0.1:8931 preview action.json
nb1 control --endpoint http://127.0.0.1:8931 run DEVICE_SERIAL action.json --wait
nb1 control --endpoint http://127.0.0.1:8931 job j_7K3XJNY9ZM
nb1 control --endpoint http://127.0.0.1:8931 cancel j_7K3XJNY9ZM --wait
```

## HTTP operations

| Request | Behavior |
| --- | --- |
| `POST /v1/preview` | Compile `action`, `sequence` or `pattern_id`, with optional `serial` capability checks. |
| `POST /v1/jobs` | Accept `action`, `sequence` or `pattern_id`, required `serial`, and optional `id`. |
| `GET /v1/jobs/{id}` | Read the accepted Job. |
| `POST /v1/jobs/{id}/cancel` | Request cancellation and read its current status. |
| `POST /v1/colors/resolve` | Resolve a `color` to exact hex. |
| `GET /v1/colors` | List locally saved color names and exact values. |
| `PUT /v1/colors/{name}` | Resolve and save a local color. |
| `GET /v1/patterns` | List Pattern ID and title summaries. |
| `GET /v1/patterns/{id}` | Read a saved Pattern and its Sequence. |
| `PUT /v1/patterns/{id}` | Compile and save one Action or Sequence with an optional title. |
| `GET /v1/devices` | List configured devices and cached state. |
| `GET /v1/devices/{serial}` | Read one configured device's cached state. |
| `GET /healthz` | Read daemon HTTP health. |
| `GET /v1/transport-metrics` | Read transport counters. |

All runtime routes use the loopback and browser-origin checks. Preview performs pure compilation and cached capability checks.

Device state contains cached RGB values with an observation timestamp. During interval playback the worker defers periodic RGB queries to preserve scheduling, so the last observation may predate the current Job. Track the Job for progress; final cleanup performs a fresh readback before a successful or cancelled terminal state. Use camera observation separately to assess the light during playback.

## CLI connection policy

The agreed default for `nb1 control` is to report that the daemon is unavailable and let the user explicitly choose to start a daemon or use direct access. It must not start a daemon or fall back to USB automatically. Starting `nb1 server` does not install a system service.

The current `control` implementation uses HTTP; standalone basic commands provide explicit direct access. A unified Action/Sequence direct entry and guided connection-failure prompt remain follow-up work under this policy.

On macOS and Linux, install the current user's login service using the running executable:

```sh
nb1 service install --devices DEVICE_SERIAL --start
```

Omitting `--binary` selects the current process's executable through the operating system. It does not search PATH for another installation. An explicit `--binary /absolute/path/to/nb1` overrides that selection. The native manager validates the file and resolves symlinks before saving the configuration. Without `--start`, installation enables login loading without requesting immediate startup.

Cross-platform `service render` still requires an explicit target `--binary` path. Windows service installation remains unsupported. Automatic path selection does not implement service upgrade migration: package version paths, including Homebrew Cellar targets, still need upgrade and rollback qualification.

## Job IDs and deduplication

An omitted or empty `id` generates `j_` followed by ten random Crockford Base32 characters. Custom IDs accept 1–64 ASCII letters, digits, hyphens and underscores. The CLI generates its ID before submission so it can report that ID when a response is lost.

The daemon keeps each active Job. Terminal Jobs remain in a cache for up to thirty minutes and may be evicted sooner when the cache is full. Job lookups, matching duplicate submissions and terminal cancellation queries update their LRU order while leaving the original expiration time intact. A duplicate ID with the same device and JSON input returns the existing Job with HTTP 200. Different input or device returns HTTP 409. Input identity ignores object-key ordering and preserves array order and number spelling.

The cache holds up to 16,384 terminal Jobs and evicts the least recently used terminal record when that limit is exceeded. Active Jobs stay pinned and release their execution plans when they become terminal. Device ownership and per-Sequence limits govern new execution. Cache capacity does not reject new Jobs.

Deduplication applies while the original record is retained. After expiration or eviction, submitting the same ID can start a new Job. The records belong to the current daemon process's memory.

## Local colors and Patterns

The daemon uses `--library-dir`, `NB1_LIBRARY_DIR`, or the current user's `nb1/library` configuration directory. Reads return saved records or empty lists. A write creates the directories and replaces one record atomically. The library supports 256 named colors and 128 Patterns, with records up to 1 MiB and titles up to 256 Unicode characters. Writer contention is bounded to two seconds.

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

The source delivery includes the core execution path, local library, HTTP operations above, CLI commands, embedded Web controls and MCP adapter. Software checks cover the integrated routes and Agent workflows. Finite CLI/HTTP checks on the local macOS 27.2 / MK2 FW204 candidate cover serial and interval playback, Orbit, Job replay, busy rejection, cancellation, final RGB readback and ownership. Web and MCP physical acceptance, broader platform/device qualification, and replacement of the daily installation remain pending. DeviceSequence routes are planned. The [terminology table](api-v1-terminology.md) governs names across those surfaces.
