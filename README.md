# nb1

Control blink(1) from a CLI, or run a local daemon with shared, versioned HTTP APIs. High-level light actions and low-level programs use the same per-device execution worker.

Replace `20005601` with the serial of your selected device. Start the daemon in one terminal:

```sh
nb1 server --devices 20005601
```

Use the shared interface in another terminal:

```sh
nb1 control devices
nb1 control set 20005601 '#3366ff' --wait
nb1 doctor
```

`control` defaults to `http://127.0.0.1:8931`; use `--endpoint` for another local port. Explicit `control --mode direct` runs through the same core and ownership lock. `doctor` reads version, daemon, and configured device snapshots without scanning or opening USB.

Legacy `on`, `off`, `blink`, `fade`, `play`, `read`, and `write` commands still access devices directly. For example, `nb1 --device 20005601 on` does not use the daemon. Use daemon-mode `control` while a daemon owns the device.

Run as an ordinary user with the platform's device access configured. The HTTP server listens only on loopback. See `nb1 --help`, `nb1 server --help`, `nb1 control --help`, and `nb1 doctor --help`. User-service capabilities and platform limitations are documented separately; starting a daemon does not install an OS startup service.

## Interfaces and status

- The [English API reference](docs/api-reference.md) covers High level and Low level behavior, all 44 canonical HTTP operations, 111 schema models, and 14 JSON examples. [OpenAPI](docs/openapi.json) and [captured MCP schemas](docs/mcp-tools.schema.json) describe field contracts; schemas do not prove runtime support or physical behavior.
- [MCP adapter](adapters/mcp/README.md) implements real stdio MCP to local HTTP, without wrapping the CLI or owning USB.
- [Shutdown diagnostics](docs/daemon-lifecycle-diagnostics.md) explain the candidate's bounded lifecycle logs and their limits.
- [Agent skill](skills/blink1-control/SKILL.md) and [llms.txt](llms.txt) explain expression selection, original receipts, host-side pattern storage, and recovery.
- [Chinese reference manual](docs/api-reference.zh-CN.md), [Chinese CLI guide](docs/patterns-cli.md), and [Chinese README](README.zh-CN.md) are localized references.

The qualified local baseline96a includes eight v1 actions, experimental Orbit, Program/FrameProgram, advanced host Pattern storage, seven advanced CLI commands, MCP16, and an embedded Animal Crossing-inspired Pattern Book. A later source candidate adds advanced list as an eighth CLI command, four MCP authoring tools (MCP20), opt-in transport diagnostics, and a pinned English Web debugger bundle; discover the installed version before using candidate features.

The baseline has finite Mac/MK2 CLI/MCP/Web evidence and workspace installation checks. This documentation does not announce a new release, global MCP registration, OS service installation, or qualification of MK3 and other-OS physical USB. Candidate qualification and distribution work remain separate from the installed baseline. The local planning site is an internal review artifact.
