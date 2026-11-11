# nb1

Control blink(1) from a CLI, local HTTP, MCP or an embedded Web workbench. High-level Actions compile into low-level Sequences and use the same per-device worker.

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

Starting `server` does not install a startup service. Run it in a terminal, manage it as an ordinary background process, or explicitly configure the platform's user-service mode. Inspect `nb1 service --help` for available operations. No keyboard or input-monitoring permission is needed to control the lamp.

## Interfaces

- [API v1 reference](docs/api-v1.md): Actions, Sequences, Jobs, colors, Patterns and limits.
- [Terminology](docs/api-v1-terminology.md): names shared by all interfaces.
- [MCP adapter](adapters/mcp/README.md): real stdio MCP to local HTTP.
- [Agent skill](skills/blink1-control/SKILL.md) and [llms.txt](llms.txt): guidance for AI clients.

Actions are `set`, `off`, `fade`, `blink`, `breathe`, `pulse`, `alternate`, `orbit` and `rainbow`. Colors accept names, Hex, RGB8, HSV/HSB and local `@name` references. Patterns are saved on the computer. Native DeviceSequence storage has separate limits and deployment requirements.

MCP and the bilingual Web workbench are being aligned with this contract. Independently maintained [nb1-web](https://github.com/b1ug/nb1-web) source becomes a pinned bundle embedded in nb1. The [nb1-site](https://github.com/b1ug/nb1-site) repository contains initial documentation website content; both repositories are private during development.

Software, physical USB and optical checks are distinct. Local MK2 results do not establish MK3, Linux/Windows USB or nonvolatile storage. A development commit does not imply a tag or Release.
