# Per-user application configuration

This package loads typed configuration for the current CLI and v1 daemon. It
does not select USB hardware, start a daemon, write library records or move locks.

`Select(flagPath, envPath)` chooses `--config`, then `NB1_CONFIG`, then
`config.yaml` in the shared `userconfig.AppDir()` directory. It returns absolute
paths and a `Required` marker. It does not create directories. A missing default
file uses defaults; a missing explicitly selected file is an error.

The default application directory is:

| System | Directory |
| --- | --- |
| macOS and Linux | `$XDG_CONFIG_HOME/nb1`, or `~/.config/nb1` |
| Windows | `%AppData%\nb1` |

Selecting another configuration file does **not** relocate the default library
or ownership directory. Locks remain under the existing native OS application
root's `device-locks` directory; on macOS that is still
`~/Library/Application Support/nb1/device-locks`. `XDG_CONFIG_HOME` must be absolute; a relative
value is an error even if `--config` explicitly selects a different file. Older
macOS profile/library locations are not aliases or automatically migrated. See
[configuration and coordination roots](../userconfig/README.md) before operating a
candidate alongside an older daemon.

```yaml
device: ""
server:
  devices: []
  listen: "127.0.0.1:8931"
jobs:
  terminal_limit: 16384
  terminal_ttl: 30m
library_dir: ""
```

`device` is empty or an exact eight-digit hexadecimal serial string. Quote
numeric-looking serials. `server.devices` contains up to 64 distinct serial
strings; it does not discover connected devices. `server.listen` must use a
loopback IP or `localhost` and a numeric port in 0–65535. Service installation
can impose a nonzero-port requirement separately.

Both Job limits must be positive. `terminal_ttl` is a Go duration string, such as
`30m` or `1h30m`, measured from terminal publication. It is a maximum retention
duration: the count limit can evict terminal Jobs sooner. Daemon restart also
discards Jobs. An evicted ID can be accepted and executed again; these settings
do not promise a guaranteed deduplication window.

An empty `library_dir` uses the stable application directory's `library`
subdirectory. An override must be absolute. The existing `libraryv1` store owns
`colors/` and `patterns/`; loading this configuration does not create either.

`Load(selection)` starts from `Defaults(selection.BaseDir)` and rejects malformed
YAML, unknown fields, duplicate keys, aliases, merges, incorrect scalar types,
multiple documents and invalid settings. Files must be regular, not symlinks or
FIFOs, and at most 1 MiB; reads are bounded. The original `setting.yaml` fields
`title`, `base_url`, `port`, `content_dir` and `color` are accepted as opaque YAML
for the existing Viper commands, but are excluded from the typed `Config` output.
No recent experimental configuration fields are recognized. `NB1_DEVICE` remains
the responsibility of the existing CLI integration; nested environment variables
are not automatically imported here.

`Template()` supplies a documented example without a selected device.
`Init(path)` explicitly creates its private parent directory and a new 0600 file,
refuses existing files, and creates no library directory. Existing parent
permissions are never changed. On Windows, directory ACLs are inherited rather
than represented by POSIX mode bits.

Verification: `go test -race ./internal/appconfig`.
