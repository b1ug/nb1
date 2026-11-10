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

- The [English API reference](docs/api-reference.md) covers High level and Low level behavior, the 44 captured canonical HTTP operations, 111 schema models, and 14 JSON examples. [OpenAPI](docs/openapi.json) and [captured MCP schemas](docs/mcp-tools.schema.json) describe field contracts; schemas do not prove runtime support or physical behavior. The bilingual Web candidate adds two static GET resources beyond that captured HTTP inventory; see its [embed and localization notes](docs/web-localization.md).
- [MCP adapter](adapters/mcp/README.md) implements real stdio MCP to local HTTP, without wrapping the CLI or owning USB.
- [Shutdown diagnostics](docs/daemon-lifecycle-diagnostics.md) explain the installed runtime's bounded lifecycle logs and their limits.
- [Agent skill](skills/blink1-control/SKILL.md) and [llms.txt](llms.txt) explain expression selection, original receipts, host-side pattern storage, and recovery.
- [Advanced Pattern snapshot exchange](docs/pattern-snapshots-v2.md) describes the development CLI's host-only import/export commands and read-only future-recipe backups.
- [Chinese reference manual](docs/api-reference.zh-CN.md), [Chinese CLI guide](docs/patterns-cli.md), and [Chinese README](README.zh-CN.md) are localized references.

The installed workspace source `1e2e7650e2cff6686d95cb9908953e5fd370f2bc` includes eight advanced Pattern CLI commands (including list), MCP20, opt-in transport diagnostics, and the pinned English Animal Crossing-inspired Web debugger. Its high-level actions and host Pattern library retain the same per-device core. The prior [96a MCP16 snapshot](docs/mcp-tools.daily-96a.schema.json) remains available; all 16 complete tool objects are unchanged in MCP20, whose four additional authoring tools remain experimental.

This development source embeds the fixed bilingual Web producer `1e1b1ddf2a985dea00e494761eed592183755c1d`. All three tools default to English and offer explicit Chinese selection. This source candidate has not replaced the installed workspace binaries.

The installed binary has finite Mac/MK2 evidence across two windows: five CLI/MCP tasks (three completed, two cancelled), plus a separate read-only English Web recheck. The first window's overall failure from a Web-check route assumption is preserved. Installation then passed 38 read-only checks. This does not announce a release, global MCP registration, OS startup service, or qualification of MK3, other-OS physical USB, Flash persistence, or long-running reliability. Some legacy program messages remain Chinese; further English help changes in source are not part of this binary. The local planning site is an internal review artifact.
