# Daemon validation and platform limits

[简体中文](daemon-validation.zh-CN.md)

This is a dated validation snapshot for the development candidate, not a release
or a claim of physical USB support on every platform.

## One executable, explicit modes

`nb1` contains both the CLI and the daemon. `nb1 server --devices SERIAL` runs in
the foreground. Installing the executable does not install or start a service.
On macOS and Linux, `nb1 service install --devices SERIAL` installs a current-user
login service without starting it immediately. Add `--start`, or explicitly run
`nb1 service start`, to start it now. Omitting `--binary` selects the current
executable. Windows automatic service installation is not implemented.

`nb1 control ...` uses the configured loopback HTTP endpoint. If the daemon is
unavailable, it reports the failure and explains the explicit choices; it does
not start another daemon or silently switch to USB. Use `nb1 control --direct
run SERIAL FILE` for explicit foreground playback without a daemon. Direct
access shares the native per-device ownership lock with the daemon.

## Evidence recorded on 2026-11-12

| Target | Actual foreground daemon, CLI and embedded Web | Managed service |
| --- | --- | --- |
| macOS / Apple Silicon | Native Actions: 16 functional checks and cleanup passed | Local macOS 27.2: 18 LaunchAgent checks and 24 isolated Homebrew upgrade checks passed |
| macOS / Intel | Native Actions: 16 functional checks and cleanup passed | Not qualified on an Intel machine |
| Linux / amd64 | Native Actions: 16 functional checks and cleanup passed | Ubuntu 24.04: actual Homebrew install, upgrade, rollback and cleanup passed under a real user manager |
| Linux / arm64 | Native Actions: 16 functional checks and cleanup passed | Ubuntu 24.04.5 VM: 33 checks, including reboot followed by user-login startup and retained local library |
| Windows / amd64 | Native Actions: 16 functional checks and cleanup passed, including targeted CTRL_BREAK | Render and read-only status only; automatic install/start/stop/uninstall remain unimplemented |

The [five-platform candidate run](https://github.com/b1ug/nb1/actions/runs/43186037056)
uses source `45d92e75bd68e92f815aab3157d3d5b6c34d7f92`. It builds each
native CGO executable from a clean checkout, binds the package to its source and
dependency versions, and runs that exact executable. Each platform's receipt
includes its binary digest. All 80 functional checks and five cleanup checks
passed without forced process termination. These runs explicitly require no
blink(1) attached.

The [Linux Homebrew service run](https://github.com/b1ug/nb1/actions/runs/43190346262)
uses source `b82705404e9ba0a750bec1c3ed5229144ec0cd00`. It records 79 lifecycle
and refusal-test entries, all six expected refusal cases, 24 package-upgrade
checks, and cleanup of its temporary service, package, tap and account. It uses
an isolated development tap; no public package or release was published. The
checker keeps the running Linux user manager's configuration root, while its
explicit profile, color/Pattern library and locks stay in private fixture paths.
The upgrade and rollback checks confirm identical profile and library bytes.
The [earlier run](https://github.com/b1ug/nb1/actions/runs/43182659126) remains a
separate 19-check upgrade snapshot at source `b3b5ac2`.

The current macOS Homebrew check builds two development packages from source
`ec82faa273754c289ec0006d0d044379f1ba3b2a` in a fresh private prefix, using
Homebrew source `bf464f57275cae0be106b43d190c98564ac59b0e` and Go 1.27.4.
All 24 checks passed: the stable service entry, captured XDG/profile/lock paths,
upgrade without implicit restart, old-keg removal, explicit restart, rollback,
and unchanged color/Pattern records and service definition. Its service, tap,
packages and private prefix were removed. This does not qualify installation
into the host's default `/opt/homebrew` prefix, which was left untouched.
The [upgrade checker](../packaging/homebrew/check_upgrade_service.py) records
process, package and cleanup results separately.

The foreground checks exercise an explicitly absent configured serial, health,
CLI state, pure preview, rejected offline execution, embedded HTML/JavaScript/
font assets, port conflict, foreign Origin refusal, and color/Pattern retention
after process restart from another explicit executable path. They also verify
normal signal-driven exit and that an unavailable CLI call leaves the daemon
stopped. See [the acceptance harness](../ci/README.daemon-service.md).

## Installation and persistence details

The Linux VM kept `Linger=no`. After a confirmed reboot, the enabled user service
became healthy after the first user session, without another explicit service
start. This qualifies login startup, not unattended startup before login. The
VM used an ordinary guest user and no physical USB passthrough. Its service,
VM and task state were removed after verification.

Managed-service installation checks executable and configuration ancestry.
The VM fixture initially created a group-writable `~/.local`; installation
correctly refused before starting a process. Narrowing only the fixture owner's
new directories resolved it. The installer does not change parent permissions
automatically. When this occurs on a real machine, inspect the named ancestor
and choose a trusted installation path; do not recursively change an existing
shared directory's permissions.

Local colors and Patterns persist on disk. Job status and duplicate detection
belong to one daemon process and are cleared on restart. A retained Pattern does
not cause automatic playback after restart. Package upgrade requires an
explicit service restart to enter the new executable; the acceptance run checks
the stable Homebrew entry rather than an expiring Cellar version path.

## Remaining boundaries

These results do not qualify Linux/Windows physical USB, MK3 hardware, device
flash, unplug/reconnect, or indefinite reliability. The local physical-device
evidence is limited to macOS and MK2/FW204. An absent-device signal test cannot
prove that a blocked native USB call can be forcibly interrupted. Client and
shutdown waiting budgets are not native I/O hard deadlines. A missing
best-effort shutdown log record is reported as unobserved, not as proof of
hardware cleanup.
