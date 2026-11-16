# Charm CLI presentation and logging

Status: draft design for review. This proposal changes no executable code,
dependencies, Go version, API contract, or installed service. Experimental
programs run outside the product tree and do not access hardware.

## Problem and intended behavior

nb1 needs clearer terminal help, errors, and diagnostics while remaining a
predictable command-line/API tool. Evaluate Fang for Cobra presentation and
Charm Log for application diagnostics in a separate change from the ongoing
runtime, Web, Pattern, and device-routing work.

The intended boundary is:

```text
English API errors and structured application events
                    |
          application presentation boundary
                    |
       +------------+----------------+
       |                             |
Human CLI help/errors          Structured machine output
optional locale + styling      English contracts, no ANSI
       |
Charm terminal diagnostics on stderr
```

All HTTP, MCP, and public Go API identifiers, error codes, state values, and
system-generated messages remain English. User-authored names and content are
returned unchanged. Optional CLI localization affects human-facing descriptions
and errors; it does not translate protocols, command names, flags, IDs, or
structured log keys. Fang is not an internationalization framework.

## Existing contracts to preserve

The source audit covers the public `master` baseline `cb0c21f` and the separate
Nightlab candidate `7073bcc`. This proposal branches from `master`; it does not
include or merge the Nightlab PR.

| Output or behavior | Required result |
| --- | --- |
| `control` command results | Keep the existing JSON stdout, including its default output mode. |
| `version --json`, `doctor --json`, service JSON modes | Keep valid, unadorned JSON and the existing field/version semantics. |
| `service render` | Keep stdout as the rendered configuration; no decorative output. |
| `server` runtime events | Keep its existing JSON writer, lifecycle records, limits, and dropped-event accounting. |
| Independent `nb1-mcp` | Keep stdout exclusively for MCP JSON-RPC; diagnostics remain on stderr. |
| Human errors and ordinary diagnostics | Send to stderr; no duplicate error rendering or automatic usage dump. |
| Shutdown and command exit | Finish application cleanup before the top-level exit; preserve nonzero error exits and unknown-outcome recovery information. |

The daemon's bounded event/transport writer is separate from hlog/zap. Replacing
the CLI logger must not rewrite this stream or synchronously log from a device
worker. A blocked diagnostic sink must not prevent hardware cleanup.

Current application file logs use JSON, include PID and source metadata, rotate
at 10 MiB, compress rotated files, and retain unlimited backup history when the
configured backup count is zero. Preserve those behaviors in the first runtime
slice. A different file schema or retention policy requires an explicit change;
a terminal logger alone is not a rotation mechanism.

Relevant implementation areas include `cmd/root.go`, `cmd/runtime_log.go`,
`cmd/runtime_shutdown.go`, `cmd/transport_log.go`, `cmd/service.go`,
`internal/service/systemduser/runner.go`, and `adapters/mcp/main.go`.

## Upstream versions and dependency decision

Inspect versioned releases rather than assuming the README or `main` branch
matches a release:

| Candidate | Import path | Declared Go minimum | Consequence |
| --- | --- | --- | --- |
| Fang v2.0.1 | `charm.land/fang/v2` | 1.25.0 | Its module also raises Cobra to at least 1.9.1, from nb1's current 1.7.0. |
| Charm Log v2.0.1 | `charm.land/log/v2` | 1.25.8 | Adopting the current pair raises nb1's declared minimum patch version. |
| Charm Log v1.0.0 | `github.com/charmbracelet/log` | 1.21 | A lower declared minimum does not establish compatibility with Fang v2's dependency graph. |

The inspected Fang `main` declares Go 1.26, but **v2.0.1 declares 1.25.0**.
Do not use the development branch's requirement to describe the release.

Prefer evaluating the same-generation v2 pair. Do not silently change nb1's
current Go 1.25.0 floor or assume that pinning an older Log release solves it.
A runtime implementation must make the minimum-version decision explicit and
verify the complete root and independent MCP module graphs. A mixed
Fang-v2/Log-v1 probe already fails to build because minimal version selection
combines the newer ANSI package with older Lip Gloss/cellbuf APIs.

The design PR itself keeps all module files unchanged. Before a runtime patch,
record a reproducible build on the chosen minimum toolchain, dependency changes,
and binary size, rather than selecting versions by the number of direct imports.

## Proposed integration

### 1. Introduce a small application logging boundary

Use the standard `log/slog` API for new application-facing structured events.
Charm Log can provide the human stderr handler. Keep log levels, named fields,
PID, and existing `--log-level`, `--debug`, and `--log-file` behavior explicit.
Do not expose Charm-specific logger types to the device SDK or add logging
framework dependencies to GID, blink1-go, or colorkit.

Console and file destinations require separate handlers/encoders. Do not send a
single decorated byte stream through `io.MultiWriter` to both terminal and file.
Keep the existing file encoder/rotation behavior behind an adapter in the first
runtime slice; migrate that sink only with a documented schema comparison.
Convert existing `zap.Field` values deliberately at the remaining legacy call
sites instead of passing them unchanged as new logger key/value arguments.

Avoid a repository-wide mechanical rewrite. The initial application boundary,
root logger construction, and necessary legacy callers are enough. Existing
unused logger setters should be reviewed before adding another shared global.
Do not use Charm `Fatal`/`Fatalf` or panic to terminate a path that still owes
USB, worker, or file cleanup.

### 2. Add a presentation adapter around Cobra

Preserve nb1's own checked version command and build metadata. If Fang is
selected, disable its automatic version, completion, and manpage additions;
these should not change merely because help is restyled. Do not install another
signal handler over server/direct lifecycle handling.

Human errors should be rendered once, after application cleanup. The renderer
must retain the original error code, device/Job ID, endpoint, and recovery
command. It must also handle a cleanup error when the command itself succeeded.

Keep machine, daemon, MCP, configuration-rendering, and no-color paths outside
the enhanced presentation path. Piped output, `NO_COLOR`, and `TERM=dumb` must
remain plain and predictable. Preserve existing output writers rather than
hardcoding all command output to `os.Stdout`.

### 3. Resolve Fang's terminal-probing gate before enabling it

In v2.0.1, `theme.go` checks the process stdout and, when it is a terminal, calls
`lipgloss.HasDarkBackground(os.Stdin, os.Stdout)` while preparing help/error
styles. The custom color-scheme and error-handler hooks run after this style
preparation. A custom palette therefore does not by itself disable the probe;
`NO_COLOR` is not a guard in that helper either.

This is terminal I/O, not a request for macOS Input Monitoring permission.
Nevertheless, a presentation change must not unexpectedly consume input,
introduce a terminal round trip on machine/error paths, or delay cleanup.
The isolated experiment uses ordinary pipes and does not validate this terminal
behavior.

The runtime patch needs an explicit non-query style path before using Fang for
interactive presentation. Prefer a suitable upstream option/version when one
exists. Until then, retain the current Cobra path for affected modes. Do not
work around this by swapping process-global stdin/stdout, adding a hidden fork,
or requesting keyboard permissions. Charm Log adoption can be reviewed
independently of this Fang gate.

### 4. Compose with localization at the application boundary

Future locale selection must precede help generation and argument errors, not
only a Cobra pre-run hook. Keep `version --json` independent of runtime profile
loading and preserve help/recovery behavior with a broken profile.

Translate known message keys and structured parameters, not English
`err.Error()` strings. Keep unknown operating-system/third-party diagnostics
as the original detail. Protocol output and persistent diagnostic messages stay
English. Preserve the fixed `LC_ALL=C` and color settings used when parsing
service-manager output; do not change the process-global locale.

## Isolated experiment results

The design experiment used ordinary pipes, stdin connected to the null device,
and pure-Go temporary programs outside the product checkout:

| Combination | Actual toolchain | Observed result |
| --- | --- | --- |
| Cobra 1.7.0 + Charm Log v1.0.0, without Fang | Go 1.25.0 | Build and 10 stream/exit/JSON checks passed. |
| Fang v2.0.1 + Charm Log v2.0.1 | Go 1.25.9 | Build and 13 checks passed, including disabled automatic commands. |
| Fang v2.0.1 + Charm Log v1.0.0 | Go 1.25.0 | Build failed with the mixed ANSI/cellbuf dependency APIs. |
| The same v2 module on Go 1.25.0 | Go 1.25.0 | Rejected because the selected Log module requires at least 1.25.8. |

The 23 passing checks cover help, plain JSON stdout, errors on stderr, exit
status, cleanup callbacks, `NO_COLOR`, `slog` handling, and a separate JSON file
formatter. They do **not** test the real nb1 command tree, an interactive TTY,
file rotation/retention, blocked sinks, services, MCP sessions, or USB. The
successful v2 build used 1.25.9, so it is not proof of a build on the exact
minimum 1.25.8 toolchain.

Do not quote the temporary programs' module counts or binary sizes as an nb1
increment: the programs and toolchains differ, and no production integration
has been built. Keep the negative build evidence when choosing a version set.

## Validation required for the runtime PR

Reuse existing tests grouped by responsibility. Extend the existing root JSON,
version, runtime logging, shutdown, and transport test files; do not create one
small test file per formatting fix.

| Case | Required observation |
| --- | --- |
| Help and argument error | Readable output, one error, correct exit status, no device access. |
| Existing JSON commands with debug on | Exactly the expected JSON on stdout; diagnostics only on stderr. |
| Invalid/unreadable profile | Version JSON and recovery commands preserve their current behavior. |
| JSON/file logs, pipes, `NO_COLOR`, `TERM=dumb` | No ANSI bytes; no terminal queries or stdin reads on excluded paths. |
| Application error plus cleanup | Cleanup runs before final rendering/exit; no `Fatal` short circuit. |
| Unknown accepted Job outcome | Original Job ID and endpoint survive unchanged; no automatic resubmission. |
| File rotation | JSON fields/types, size threshold, compression, retention, and close/flush are explicitly verified. |
| Slow/broken diagnostic sink | Existing bounded daemon logging and cleanup isolation still hold. |
| Root and independent MCP modules | Resolve/build with the chosen minimum toolchain; no accidental protocol decorations. |
| Native candidate packaging | Existing platform suite and checked version information remain valid. |

A synthetic Cobra/logging probe is useful evidence for API selection and stream
separation, not acceptance of nb1, live terminal behavior, service installation,
or USB timing. A presentation-only change should not restart the user's daemon
or repeat unrelated optical/Flash tests.

## Scope and review order

1. Review this design, the dependency/toolchain choice, and the probe results.
2. Implement the application logger boundary with stream/file compatibility.
3. Integrate only the Fang presentation modes whose terminal-I/O contract has
   been resolved; keep the existing application lifecycle in control.
4. Run the applicable output, cleanup, module, and candidate checks.

Keep this work separate from Pattern/Trigger routing, queue semantics, HTTP API
changes, Web appearance, full CLI localization, and foundation-library releases.
No migration aliases for recent experimental interfaces are introduced.

## Primary references

- [Fang v2.0.1 module](https://github.com/charmbracelet/fang/blob/v2.0.1/go.mod)
- [Fang v2.0.1 execution/options](https://github.com/charmbracelet/fang/blob/v2.0.1/fang.go)
- [Fang v2.0.1 theme/probing](https://github.com/charmbracelet/fang/blob/v2.0.1/theme.go)
- [Charm Log v2.0.1 module](https://github.com/charmbracelet/log/blob/v2.0.1/go.mod)
- [Charm Log documentation](https://github.com/charmbracelet/log)
