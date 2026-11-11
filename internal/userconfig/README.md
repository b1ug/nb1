# Shared user storage root

`RootDir()` resolves `$XDG_CONFIG_HOME` on macOS and Linux, or `~/.config` when
the variable is empty. An explicit XDG root must be absolute. Invalid or relative
values fail; they are never interpreted relative to the current directory and
never trigger another storage fallback. Windows retains `os.UserConfigDir()`
(`%AppData%`); XDG settings do not override the Windows native location.

`AppDir()` appends `nb1`. Both functions only resolve strings. They create no
directories, inspect no USB devices and migrate no files. The same resolver is
used for the current configuration, its default library,
and the Linux user-service configuration root. The Linux installer pins its
verified root in the child process environment.

The fixed native LaunchAgent and service-management coordination locations are
OS integration state; they are not moved by this resolver. Only the established
`setting.yaml` lookup remains a legacy application configuration fallback. No
recent experimental storage location is an alias for this root.

`NativeAppDir()` retains `os.UserConfigDir()/nb1` for persistent device ownership
coordination. On macOS the lock directory therefore stays at
`~/Library/Application Support/nb1/device-locks`, even though user configuration
and the default library now use XDG storage. Existing executables and new
candidates continue to contend on the same default lock. On Linux the native
root already follows XDG; on Windows it remains `%AppData%`.

Changing `--config` never relocates locks. Explicit `NB1_LOCK_DIR`/`--lock-dir`
still requires every controller of the same device to use the same directory.
Do not remove lock files or move directories while an owner is alive. This code
performs no automatic old-root probing, dual-lock acquisition, migration or
service restart.
