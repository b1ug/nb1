## nb1 v0.1.3

CLI commands and the embedded Workbench now choose a predictable default when
several lights are configured. The order is ascending device serial, with the
first online device selected by default.

- `nb1 control run` and `nb1 control state` honor an explicit serial, then
  `--device`, `NB1_DEVICE` and the profile's `device` setting. Automatic selection
  with multiple configured devices reports the count and chosen serial on stderr.
  The command's JSON stays on stdout. If every device is offline, the command
  uses the first serial and reports that device's state or submission error.
- The Workbench displays the configured count and current target. Refresh and
  language changes preserve the selection. A target that goes offline or is
  removed stays selected and the page explains how to proceed.
- Playback is directed to one serial per request. HTTP and MCP submissions
  continue to require that serial. A busy, failed or uncertain submission
  stays associated with its original device and Job ID.

### Default port changed to 5885

Starting with v0.1.3, the daemon listens on `127.0.0.1:5885` by default.
The HTTP API and embedded debugger share that listener. The CLI and the optional
stdio MCP adapter use the same address to reach the daemon.

The v0.1.2 default was `8931`. Existing profiles and installed services with
an explicit listen address keep that address. To keep using the old port, start
the daemon with `--listen 127.0.0.1:8931` and point clients at it with
`--endpoint http://127.0.0.1:8931`. To move to 5885, update any explicitly
pinned service and client settings together, restart the daemon, and update
debugger bookmarks to <http://127.0.0.1:5885/demo/>.

### Install and run

Download the matching archive, `release.json` and `SHA256SUMS` from this release.
Verify and unpack the archive into a new trusted directory, then run:

```sh
./nb1 version --json
./nb1 list
./nb1 server --devices YOUR_DEVICE_SERIAL
```

Open <http://127.0.0.1:5885/demo/>. On Windows, run `nb1.exe`. The matching MCP
adapter and Agent Skill are separate optional downloads. Start a foreground
server or explicitly manage it with `nb1 service`.

Prebuilt CLI and server binaries require macOS 15+ or Linux glibc 2.38+. Optional
MCP binaries require macOS 13+ and are static on Linux. Mac downloads are not
Developer ID notarized. Source builds require Go 1.25.8+.

The [Homebrew tap](https://github.com/b1ug/homebrew-tap) installs the version in
its current Formula. Installation and device selection are covered in the
[English and Chinese documentation](https://b1ug.github.io/nb1-guide/).
