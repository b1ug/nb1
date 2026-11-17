## nb1 v0.1.1

This patch adds Fang presentation for human CLI help and errors, uses Charm Log
for terminal diagnostics, and preserves timeout and cancellation causes during
direct Sequence playback.
CLI help and human-readable version output now select colors for their actual
output stream. Console diagnostics use Charm Log's renderer on stderr and select
colors independently.

### Fixed

- Human CLI help and errors use Fang through a temporary fixed fork based on
  upstream v2.0.1, while Cobra retains command routing and argument handling.
  Existing version metadata and machine-output contracts remain separate from
  presentation.
- Application diagnostics use the actual Charm Log v2.0.1 terminal renderer.
  Normal console output is concise; `--debug` also includes caller and PID
  details. File logs retain their existing JSON fields, timestamps, PID,
  caller and error encoding, 10 MiB rotation, compression and backup policy.
- Invalid `--log-file` directories, leaf symlinks, special files and initially
  inaccessible or uncreatable targets are rejected before command execution,
  keeping existing directory contents untouched. Internally reported writer
  failures use Charm diagnostics on stderr; the JSON file schema and rotation
  policy remain unchanged.
- A nonempty `NO_COLOR` makes human help, errors, the Logo and console logs
  fully unstyled, including when `CLICOLOR_FORCE` is also set.
- Pipes and `TERM=dumb` use plain text by default, including a dumb terminal
  with a leftover `COLORTERM=truecolor`. `CLICOLOR=0` disables ordinary color;
  use `CLICOLOR_FORCE=1` or `true` to request it when `NO_COLOR` is unset.
  Force values follow Go's boolean parsing: `false`, `0` and invalid values
  such as `2` use the ordinary terminal rules rather than forcing color.
- Human output respects the selected writer rather than a Logo cached at
  process initialization. JSON command results keep their existing unstyled
  stdout contract; diagnostics remain on stderr.
- Direct Sequence playback preserves the original timeout or cancellation
  cause when its Job becomes cancelled before the caller observes the context.
  Submission and Job failures retain their existing behavior, and cleanup
  errors still take precedence. This does not make native I/O cancellable.
- Homebrew source builds disable unrelated parent-repository VCS stamping.
  The Formula renderer also emits the corrected strict Homebrew style, so new
  releases can use their own renderer without the v0.1.0-specific tap adapter.

CLI help and application-generated diagnostics remain English under English,
Chinese, Japanese, French and other locale settings. This patch adjusts output
styling; the bilingual embedded Web workbench has its own language switch.
HTTP, MCP and Go identifiers retain their existing English contract.
The nb1 and standalone MCP source minimum is Go 1.25.8, required by the selected
Charm Log release. Foundation-library requirements are unchanged. CLI
localization remains separate work. Command JSON, MCP JSON-RPC and the daemon's
separate JSON event stream remain unstyled.

### Fang dependency and upstream fixes

The public dependency is `github.com/vt128/fang/v2` at
`v2.0.2-0.20261117154700-ea7a74892849`, fixed to
[commit ea7a7489284927655a069bd4da792c25a0259450](https://github.com/vt128/fang/commit/ea7a7489284927655a069bd4da792c25a0259450).
This temporary fork backports an explicit `WithDarkBackground(bool)` option
onto official v2.0.1. The option lets callers select a known background without
automatic terminal detection; leaving it unset preserves upstream behavior.
The separate proposal for official Fang is [upstream PR #111](https://github.com/charmbracelet/fang/pull/111).
The fork also keeps custom optional arguments in their authored positions:
`control run [SERIAL] FILE` is displayed in the same order that the CLI executes.
This presentation correction is proposed separately in [upstream PR #112](https://github.com/charmbracelet/fang/pull/112);
command definitions and argument parsing are unchanged.
nb1 consumes the fork as a normal public module with an exact version and
checksum, without a Fang `replace` directive. Its source and license notices
are included in the release manifests.

### Install and start

Download the matching v0.1.1 CLI archive, `release.json` and `SHA256SUMS` from
this release. Verify the archive and unpack it into a new trusted directory:

```sh
./nb1 version --json
./nb1 list
./nb1 server --devices YOUR_DEVICE_SERIAL
```

Open `http://127.0.0.1:8931/demo/`. The CLI, optional server and Nightlab
workbench are one executable. The matching MCP adapter and Agent Skill are
separate downloads. On Windows the executable is `nb1.exe`.

The public [Homebrew tap](https://github.com/b1ug/homebrew-tap) installs the
version currently published by that tap with `brew install b1ug/tap/nb1`.
Check `nb1 version` before assuming its Formula has advanced to this release.
Homebrew builds released source; services are explicitly managed with
`nb1 service`. The [English and Chinese documentation](https://b1ug.github.io/nb1-docs/)
includes installation, the API and Agent setup.

### Runtime and scope

The prebuilt CLI/server binaries declare a minimum of macOS 15.0 and require
glibc 2.38 or later on Linux. Optional MCP binaries declare macOS 13.0 and are
statically linked on Linux; they still require a compatible nb1 server.
The macOS downloads are not Developer ID notarized. These prebuilt requirements
are separate from Homebrew source-build compatibility.

The source commit, Go/VCS/module information, embedded Web manifest, notices,
archive hashes and platform qualification are recorded in `release.json`.
Foundation dependencies remain blink1-go v0.0.2, GID v0.0.3 and colorkit v0.1.0.
Software and container checks remain separate from physical USB, MK3, optical,
login/reboot or nonvolatile-storage qualification. Windows supports foreground
operation; automatic Windows service management remains separate work.

The current public API rejects a new Job while its device is busy. Editable
source libraries, Trigger routing, composition, queue/preemption and new native
sequence APIs remain separate work. Installing a package does not automatically
start/restart a daemon, install a service or migrate user content.
