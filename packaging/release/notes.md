## nb1 v0.1.1

This patch adds Charm Log terminal diagnostics, corrects terminal styling found
during Linux Homebrew acceptance, and preserves timeout and cancellation causes
during direct Sequence playback.
CLI help and human-readable version output now select colors for their actual
output stream. Console diagnostics use Charm Log's renderer on stderr and select
colors independently. The Logo retains its RoseWater palette.

### Fixed

- Application diagnostics use the actual Charm Log v1.0.0 terminal renderer.
  Normal console output is concise; `--debug` also includes caller and PID
  details. File logs retain their existing JSON fields, timestamps, PID,
  caller and error encoding, 10 MiB rotation, compression and backup policy.
- A nonempty `NO_COLOR` suppresses Logo and console-log color, including when
  `CLICOLOR_FORCE` is also set.
- Pipes and `TERM=dumb` use plain text by default, including a dumb terminal
  with a leftover `COLORTERM=truecolor`. `CLICOLOR=0` disables ordinary color;
  an explicit nonzero `CLICOLOR_FORCE` can request it when `NO_COLOR` is unset.
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
The source minimum remains Go 1.25.0. This release retains Cobra help and does
not include Fang or a new CLI localization framework. Command JSON, MCP
JSON-RPC and the daemon's separate JSON event stream remain unstyled.

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
