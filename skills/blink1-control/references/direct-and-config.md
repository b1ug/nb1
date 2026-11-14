# Direct mode and configuration

Read this when the user explicitly chooses foreground USB execution or asks about configuration. Ordinary MCP/HTTP control does not require this mode.

## One foreground run

Only `nb1 control --direct run [SERIAL] FILE` supports direct Action/Sequence/Pattern execution. Use an explicitly selected device from the argument, root `--device`, `NB1_DEVICE` or profile `device`; direct mode does not discover a default device. Input is compiled and local colors/Patterns resolved before USB opens.

```sh
nb1 control --direct run DEVICE_SERIAL action.json
nb1 control --direct run DEVICE_SERIAL action.json --timeout 2m
```

Direct execution always waits in the foreground and starts no HTTP server. Each invocation is new playback. Do not pass `--id` or `--endpoint`: its Job belongs only to that process, with no later HTTP lookup or cross-process deduplication. Keep its ownership-lock directory consistent with every other device owner; an active daemon can cause a legitimate ownership refusal.

Direct mode has no total deadline unless `--timeout` is explicitly supplied. A deadline or Ctrl-C requests cancellation followed by a separate bounded cleanup wait, default 10s. It cannot forcibly interrupt a blocked native call.

Inspect the command exit status and JSON `mode`, `job` and `engine_closed`. `engine_closed` alone proves neither success nor an extinguished light. A successful `hold` leaves its final color. When no trustworthy result exists, report uncertainty: do not query a different daemon or automatically execute again.

## Locate configuration without USB

```sh
nb1 config path
nb1 config show
```

These inspect this process's selected settings, not a running daemon. They do not enumerate USB or start a service.

On macOS/Linux, the profile/default-library application root is `$XDG_CONFIG_HOME/nb1`, otherwise `~/.config/nb1`. A nonempty XDG root must be absolute. Windows uses native user configuration, normally `%APPDATA%\nb1`. Select a profile with `--config`, then `NB1_CONFIG`, then the default path; explicit missing/invalid profiles fail.

The daemon's library selection is `--library-dir`, `NB1_LIBRARY_DIR`, profile `library_dir`, then the stable per-user `nb1/library` default. A custom profile path alone does not relocate the library.

Device locks retain the native platform root independently of profile/library choice. On macOS the default is `~/Library/Application Support/nb1/device-locks`; inspect `device_lock_dir` rather than deriving it from the library. All owners must agree on an explicit lock-directory override.

The HTTP CLI selects its endpoint from `--endpoint` or selected profile `server.listen`, default `127.0.0.1:8931`; its default request/wait budget is 30s. MCP selects its own `--endpoint`, default `http://127.0.0.1:8931`, and does not read nb1 YAML. Use the task's actual endpoint, especially when another installation occupies the default port. Neither client starts an unavailable daemon or automatically falls back to USB.

## Device-native storage

Host Patterns use the computer's library on both old and new hardware. A host Sequence is not an uploaded DeviceSequence. Device RAM, Flash, startup effects and capacity limits require separately exposed capabilities and deployment APIs. Check actual installed support; do not replace an unsupported upload request with host-only storage while claiming the same outcome.
