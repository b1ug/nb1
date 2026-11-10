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

- The [English API reference](docs/api-reference.md) covers High level and Low level behavior, 46 installed6ca canonical HTTP operations (44 in historical1e), 111 schema models, and 14 JSON examples. [OpenAPI](docs/openapi.json) and [captured MCP schemas](docs/mcp-tools.schema.json) describe field contracts; schemas do not prove runtime support or physical behavior. The two added entries are conditional local JavaScript GETs; they add no hardware operation. The 44-operation captured baseline remains historical evidence; see its [embed and localization notes](docs/web-localization.md).
- [MCP adapter](adapters/mcp/README.md) implements real stdio MCP to local HTTP, without wrapping the CLI or owning USB.
- [Shutdown diagnostics](docs/daemon-lifecycle-diagnostics.md) explain the installed runtime's bounded lifecycle logs and their limits.
- [Agent skill](skills/blink1-control/SKILL.md) and [llms.txt](llms.txt) explain expression selection, original receipts, host-side pattern storage, and recovery.
- [Advanced Pattern snapshot exchange](docs/pattern-snapshots-v2.md) describes the development CLI's host-only import/export commands and read-only future-recipe backups.
- [Device-native sequence model](docs/device-native-sequence.md) describes the internal offline draft: explicit placement/playback checks and raw row inspection, without device execution or new API routes.
- [Experimental native reads](docs/native-inspection.md) describes this branch's paired RAM/playback HTTP, CLI and MCP candidate. It requires an explicit local SDK replacement for software review; it is not installed, released or ready for a default-module build.
- [Chinese reference manual](docs/api-reference.zh-CN.md), [Chinese CLI guide](docs/patterns-cli.md), and [Chinese README](README.zh-CN.md) are localized references.

The installed workspace source is `6ca5a14e9f7fec8a5766e79f5bdef3fafef66d8f`: ten advanced Pattern CLI commands (including explicit host snapshot import/export),MCP20 with the same complete objects, and the pinned bilingual Animal Crossing-inspired Web debugger. Its three tools default to English and support explicit Chinese selection. The46 canonical inventory adds only two local JavaScript GET resources to the historical44;111 models and14 JSON inputs are unchanged.

Controlled promotion passed57 read-only postinstallation checks; a separate four-check isolated browser pass verifies language switching without application writes. These are local workspace/read-only UI results, not new animation, optical, MK3, other-OS physical USB, Flash or long-running qualification. Prior1e/CLI8/English Web and its bounded physical windows remain historical evidence. SDK b3/GID37 and host libraries were retained. No release, global MCP registration or OS startup change is implied. The local planning site remains an internal review artifact.
