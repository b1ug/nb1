# nb1

<img src="internal/webui/dist/assets/nb1-mascot.webp" width="96" height="96" alt="nb1 mascot">

[简体中文](README.zh-CN.md)

Control blink(1) from a CLI, local HTTP, MCP or an embedded Web workbench. High-level Actions compile into low-level Sequences and use the same per-device worker.

[Try a matching candidate](docs/candidate-install.md): verify and unpack the CLI, MCP and Skill, select a device, and explicitly choose foreground or service operation.

This branch is the API v1 development candidate. Source validation and the version installed on a machine are tracked separately. Start an ordinary foreground daemon using the same executable as the CLI:

```sh
nb1 server --devices DEVICE_SERIAL
```

Put an expression in `action.json`:

```json
{"action":{"kind":"pulse","color":"lime","cycles":2}}
```

Then use another terminal:

```sh
nb1 control devices
nb1 control preview action.json
nb1 control run DEVICE_SERIAL action.json --wait
```

`control` defaults to `http://127.0.0.1:8931`; use `--endpoint` for another loopback port. The daemon owns USB while these clients communicate over HTTP. Standalone `on`, `off`, `blink`, `fade`, `play`, `read` and `write` access hardware directly and require the device to be available.

For a foreground Action, Sequence or saved Pattern run without a daemon:

```sh
nb1 control --direct run DEVICE_SERIAL action.json
```

Direct mode validates and resolves the local input before opening USB, always waits, and shares the device ownership lock with the daemon. It requires an explicitly selected device and never follows a failed HTTP submission automatically. Each invocation is new playback; `--id` is unavailable. An explicit `--timeout` bounds execution, followed by a separate cleanup wait. `engine_closed` confirms teardown only; check the Job status and command result for success. [Direct lifecycle and limits](docs/api-v1.md#cli-connection-policy)

Starting `server` does not install a startup service. Run it in a terminal, manage it as an ordinary background process, or explicitly configure the platform's user-service mode. Inspect `nb1 service --help` for available operations. No keyboard or input-monitoring permission is needed to control the lamp.

The macOS/Linux candidate provides `nb1 service install --devices DEVICE_SERIAL --start` for explicit login-service installation. It selects the running nb1 executable by default; use `--binary /absolute/path/to/nb1` to choose another executable. See [daemon platform validation](docs/daemon-validation.md) for actual installation, upgrade and reboot evidence. Windows automatic service management remains unimplemented.

Use `nb1 version --json` for the executable's build identity, Go toolchain and pinned hardware dependencies without contacting a daemon or USB device. Plain source builds report their commit; unknown identity is reported as development.

CLI diagnostics use English by default. Command results go to stdout; diagnostic logging, including `--debug`, goes to stderr so JSON results remain readable by other tools. The Web workbench provides English and Chinese.

## Configuration and storage

On macOS and Linux, configuration and the local color/Pattern library use `$XDG_CONFIG_HOME/nb1`, defaulting to `~/.config/nb1` when the variable is empty or unset. A nonempty XDG root must be absolute. Windows keeps the native `%APPDATA%\nb1` location. Use `nb1 config path` and `nb1 config show` to inspect this process's selected file and resolved settings.

Device locks follow the established native OS coordination policy. On macOS they remain in `~/Library/Application Support/nb1/device-locks`, so the new candidate and the older 6ca controller coordinate through the same default lease. Changing `--config` alone moves neither this lock directory nor the default library. Existing files are not moved automatically. Managed macOS/Linux service startup also preserves the selected XDG root for its profile and default library. [Configuration and device selection](docs/api-v1.md#user-configuration)

## Interfaces

- [OpenAPI 3.1](docs/api-v1.openapi.json): machine-readable current HTTP contract.
- [API v1 reference](docs/api-v1.md): Actions, Sequences, Jobs, colors, Patterns and limits.
- [Terminology](docs/api-v1-terminology.md): names shared by all interfaces.
- [MCP adapter](adapters/mcp/README.md): real stdio MCP to local HTTP.
- [Web presentation](docs/web-typography.md): custom language menu, local fonts, SVG icons and embedded asset boundaries.
- [Agent skill](skills/blink1-control/SKILL.md), [setup and use](docs/agent-guide.md), and [llms.txt](llms.txt): guidance for AI clients.
- [Codex turn notifications](examples/command-notify/CODEX.md): optional, preview-first notify or Stop integration through the local daemon, with one-invocation setup and recorded software acceptance.

Actions are `set`, `off`, `fade`, `blink`, `breathe`, `pulse`, `alternate`, `orbit` and `rainbow`. Colors accept names, Hex, RGB8, HSV/HSB and local `@name` references. Patterns are saved on the computer. Native DeviceSequence rows use required numeric `led` values `0` (all outputs), `1` or `2`, matching Action and Step. Device storage has separate limits and deployment requirements. [Device-native draft](docs/device-native-sequence.md)

MCP provides 12 tools for this contract. The bilingual Web workbench is embedded at `/demo/`; opening or previewing does not play a Job. The CLI and the separate MCP module require Go 1.25 or newer source tooling; release candidates are built with Go 1.27.4. The newer floor comes from patched dependencies, not a change to the device protocol. Independently maintained [nb1-web](https://github.com/b1ug/nb1-web) source becomes a pinned bundle embedded in nb1. The [nb1-site](https://github.com/b1ug/nb1-site) repository contains the bilingual documentation website. The independent [nb1-browser](https://github.com/b1ug/nb1-browser) workbench and Chrome extension use WebHID directly and require explicit device ownership handoff. These companion repositories remain private during development.

Software, physical USB and optical checks are distinct. Local MK2 results do not establish MK3, Linux/Windows USB or nonvolatile storage. A development commit does not imply a tag or Release.
