## nb1 v0.1.0

nb1 provides one local control core for blink(1): a CLI, an optional foreground
or managed daemon, an HTTP API and an embedded Web debugger. The debugger is
part of the executable; no Node server or separate Web installation is needed.

### Included

- The Nightlab workbench with English and Chinese UI, a 3D screen preview,
  optional preview, direct Play, separate colors for the two LED channels,
  local colors and saved Patterns, and curated eight-color themes.
- Set, Off, Fade, Blink, Breathe, Pulse, Alternate, Orbit and seven-color Rainbow
  Actions, plus unified host Sequence/Step authoring and finite playback.
- Device ownership coordination, multiple managed devices, short Job IDs,
  query/cancel/progress and configurable bounded terminal history.
- Explicit foreground server and direct CLI modes. Installing the executable
  does not silently install or start a service.
- macOS LaunchAgent and Linux systemd-user management. Windows foreground use
  is separate from Windows automatic service management.
- A matching optional stdio MCP executable using the official Go SDK and a
  standalone Agent Skill. MCP calls the existing daemon over HTTP.
- Released foundation dependencies: blink1-go v0.0.2, GID v0.0.3 and
  colorkit v0.1.0.

### Start the local debugger

Download the archive for your platform, verify `SHA256SUMS`, unpack it into a
trusted directory and run:

```sh
./nb1 version --json
./nb1 list
./nb1 server --devices YOUR_DEVICE_SERIAL
```

Open `http://127.0.0.1:8931/demo/`. Use the same executable's `control` commands
from another terminal, or explicitly choose `control --direct run` when no
daemon owns that device. On Windows the executable is `nb1.exe`.

### Scope

The source commit, native target, embedded Web manifest, dependency selection,
archive and executable hashes, and platform qualification are recorded in
`release.json`. Check the support matrix before treating software CI as
physical-device qualification.

The current public API rejects a new Job while a device is busy. New editable
source libraries, semantic Trigger routing, composition and queue/preemption
contracts remain separate work. Saved host Patterns are not automatically
written to device RAM, Flash or startup storage.

Native synchronous I/O has no portable hard cancellation deadline. MK3,
additional physical USB platforms, and Windows automatic service management
are not implied by the presence of a native binary archive. No automatic
daemon restart, startup playback or migration of user files is performed.

The separate WebHID workbench and Chrome extension have their own device
permissions and release process; they are not required for the embedded local
debugger.
