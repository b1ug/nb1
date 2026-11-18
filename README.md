# nb1

<img src="internal/webui/dist/assets/nb1-mascot.webp" width="96" height="96" alt="nb1 mascot">

[简体中文](README.zh-CN.md)

Control blink(1) from a CLI, local HTTP, MCP or an embedded Web workbench. High-level Actions compile into low-level Sequences and use the same per-device worker.

[Install nb1 v0.1.3](docs/install.md): download and verify the executable, start the local server, and open its embedded workbench. MCP and the Skill are optional matching downloads. [Development candidates](docs/candidate-install.md) have a separate installation contract. The public [Homebrew tap](https://github.com/b1ug/homebrew-tap) builds the currently published Formula with `brew install b1ug/tap/nb1`; check `nb1 version` for its actual version. Read the [bilingual documentation](https://b1ug.github.io/nb1-docs/) for setup and examples.

nb1 implements API v1. Source validation and the version installed on a machine are tracked separately. Start an ordinary foreground daemon using the same executable as the CLI:

```sh
nb1 server --devices DEVICE_SERIAL
```

Open `http://127.0.0.1:8931/demo/` to use the bilingual Nightlab workbench. It is embedded in nb1: no Node server, separate Web installation or browser WebHID permission is required. Play validates and sends the current draft directly; Preview is optional. Opening the page does not start playback. Use the actual listening port if you configured another one.

For CLI playback, put an expression in `action.json`:

```json
{"action":{"kind":"pulse","color":"lime","cycles":2}}
```

Then use another terminal:

```sh
nb1 control devices
nb1 control run DEVICE_SERIAL action.json --wait
```

Use `nb1 control preview action.json` separately when you want to inspect the compiled plan without playback.

`control` defaults to `http://127.0.0.1:8931`; use `--endpoint` for another loopback port. The daemon owns USB while these clients communicate over HTTP. Standalone `on`, `off`, `blink`, `fade`, `play`, `read` and `write` access hardware directly and require the device to be available.

For a foreground Action, Sequence or saved Pattern run without a daemon:

```sh
nb1 control --direct run DEVICE_SERIAL action.json
```

Direct mode validates and resolves the local input before opening USB, always waits, and shares the device ownership lock with the daemon. It requires an explicitly selected device and never follows a failed HTTP submission automatically. Each invocation is new playback; `--id` is unavailable. An explicit `--timeout` bounds execution, followed by a separate cleanup wait. `engine_closed` confirms teardown only; check the Job status and command result for success. [Direct lifecycle and limits](docs/api-v1.md#cli-connection-policy)

Starting `server` does not install a startup service. Run it in a terminal, manage it as an ordinary background process, or explicitly configure the platform's user-service mode. Inspect `nb1 service --help` for available operations. No keyboard or input-monitoring permission is needed to control the lamp.

On macOS/Linux, use `nb1 service install --devices DEVICE_SERIAL --start` for explicit login-service installation. It selects the running nb1 executable by default; use `--binary /absolute/path/to/nb1` to choose another executable. See [daemon platform validation](docs/daemon-validation.md) for the scope of installation, upgrade and reboot evidence. Windows automatic service management remains unimplemented.

Use `nb1 version --json` for the executable's build identity, Go toolchain and pinned hardware dependencies without contacting a daemon or USB device. Plain source builds report their commit; unknown identity is reported as development.

CLI help and diagnostics use English across language environments. Terminal colors follow each output stream; a nonempty `NO_COLOR` disables them. Pipes and `TERM=dumb` use plain text unless `CLICOLOR_FORCE` explicitly requests color. Command results go to stdout; diagnostic logging, including `--debug`, goes to stderr so JSON results remain readable by other tools. JSON output remains unstyled. The Web workbench provides English and Chinese.

## Configuration and storage

On macOS and Linux, configuration and the local color/Pattern library use `$XDG_CONFIG_HOME/nb1`, defaulting to `~/.config/nb1` when the variable is empty or unset. A nonempty XDG root must be absolute. Windows keeps the native `%APPDATA%\nb1` location. Use `nb1 config path` and `nb1 config show` to inspect this process's selected file and resolved settings.

Device locks follow the established native OS coordination policy. On macOS they remain in `~/Library/Application Support/nb1/device-locks`, so controllers coordinate through the same default lease. Changing `--config` alone moves neither this lock directory nor the default library. Existing files are not moved automatically. Managed macOS/Linux service startup also preserves the selected XDG root for its profile and default library. [Configuration and device selection](docs/api-v1.md#user-configuration)

## Interfaces

- [OpenAPI 3.1](docs/api-v1.openapi.json): machine-readable current HTTP contract.
- [API v1 reference](docs/api-v1.md): Actions, Sequences, Jobs, colors, Patterns and limits.
- [Terminology](docs/api-v1-terminology.md): names shared by all interfaces.
- [MCP adapter](adapters/mcp/README.md): real stdio MCP to local HTTP.
- [Web presentation](docs/web-typography.md): custom language menu, local fonts, SVG icons and embedded asset boundaries.
- [Nightlab embedding](docs/web-nightlab.md): complete local 3D, screen playback, compressed assets and the current host contract.
- [Agent skill](skills/blink1-control/SKILL.md), [setup and use](docs/agent-guide.md), and [llms.txt](llms.txt): guidance for AI clients.
- [Codex turn notifications](examples/command-notify/CODEX.md): optional, preview-first notify or Stop integration through the local daemon, with one-invocation setup and recorded software acceptance.

Actions are `set`, `off`, `fade`, `blink`, `breathe`, `pulse`, `alternate`, `orbit` and `rainbow`. Colors accept names, Hex, RGB8, HSV/HSB and local `@name` references. Patterns are saved on the computer. Device-native sequence APIs remain separate planned work; the current HTTP v1 and MCP adapter do not deploy host Patterns to device storage. [Device-native draft](docs/device-native-sequence.md)

MCP provides 12 tools for this contract. The bilingual Web workbench is embedded at `/demo/`; opening or previewing does not play a Job. Building the CLI or separate MCP module from source requires Go 1.25.8 or newer; release artifacts record the actual build toolchain and fixed dependencies. Prebuilt executables need no Go installation. The source floor reflects the selected dependencies; the device protocol and foundation libraries' own Go requirements are unchanged. The [nb1 Embedded Workbench](https://github.com/b1ug/nb1-workbench) supplies a verified static bundle embedded at `/demo/`. The [nb1 Documentation](https://github.com/b1ug/nb1-docs) repository supplies the [English/Chinese documentation website](https://b1ug.github.io/nb1-docs/), which does not request device access. Separate [nb1 Browser Tools](https://github.com/b1ug/nb1-browser-tools) use WebHID directly; their workbench is a development preview and the Chrome reminder extension remains experimental. They require explicit device ownership handoff and are not installed by nb1. [Web components and build boundaries](docs/web-components.md)

Software, physical USB and optical checks are distinct. Local MK2 results do not establish MK3, Linux/Windows USB or nonvolatile storage. A development commit does not imply a tag or Release.

## Continuous integration

The native CI matrix builds the CLI, runs software tests with the race detector,
and runs `go vet` on Ubuntu 24.04 (amd64/arm64), macOS 15 (arm64/Intel), and
Windows Server 2022 (amd64), using Go 1.26.x and 1.27.x. Separate Go 1.25.x CI
checks source with CGO disabled on Ubuntu 22.04. Locked module versions
are used with `GOWORK=off` and `-mod=readonly`.

The separate candidate workflow builds matching CLI and MCP binaries, verifies
their source identity and archives, checks MCP discovery, and exercises a
foreground daemon with an absent configured device. CI artifacts are development
candidates, not tagged Releases or installed services. Software checks do not
establish physical hardware, mk3, or optical qualification.
