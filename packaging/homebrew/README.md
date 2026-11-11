# Homebrew packaging

`nb1.rb.in` is a source Formula template for macOS and Linux. It builds the
`nb1` binary with CGO enabled and verifies modules from `go.mod` and `go.sum`.
The minimum macOS requirement applies only on macOS. The template does not
install the separate MCP binary.

There is no published tap or new release yet. The production renderer still
requires an actual versioned upstream tag archive, SHA-256, source commit and
explicit minimum macOS version. `release-input.required.json` intentionally
leaves these release inputs empty. Candidate installation tests use a separate,
local-only tap generated from a real Git archive and its measured checksum;
they do not create a release or fill the release inputs with synthetic values.

## Installation and services are separate

Installing the Formula installs the executable and documentation. It does not
register or start a service, change USB permissions, or select a device.

With an installed candidate and a known device serial, the ordinary user can
run:

```sh
nb1 list
nb1 server --devices 20000001
```

The server runs in the foreground. On a supported desktop session, use the
same executable to register a login service:

```sh
nb1 service install --devices 20000001
nb1 service start
nb1 service status --json
nb1 service stop
nb1 service uninstall
```

`install --start` explicitly requests immediate startup. Omitting `--binary`
uses the validated stable package entry in Homebrew builds and the running
executable in ordinary source builds. macOS uses the current user's
LaunchAgent; Linux requires the current user's running systemd manager.
Registration, an active process, HTTP readiness and USB readiness are separate
states. These examples describe the development candidate, not the old release.

The Formula has no Homebrew `service do` block: service management is through
`nb1 service`, and `brew services start nb1` is not provided.

## USB-free Linux container validation

A normal container can validate installation and useful runtime behavior even
without a device. It usually has no login session or systemd user manager;
`nb1 service install` must then return a controlled error rather than assume the
unit is absent or report successful installation. It must not create a unit or
enable link after the initial manager query fails. Run `nb1 server` directly in
that environment.

The local candidate validation uses a clean Ubuntu 24.04 arm64 container with
the default Linux Homebrew prefix, a fixed Homebrew source commit, and a real
CGO build. The container runs as an ordinary user, drops all capabilities, has
no host mounts or USB passthrough, and does not expose host ports. The install/upgrade exercise retains network
access for package and module downloads. These are the checks:

1. Install a local tap with `brew install --build-from-source nb1/local/nb1`.
2. Run `brew test nb1/local/nb1` and `brew linkage --test nb1/local/nb1`.
3. Run installed `nb1 list` and `nb1 list --firmware`: both should succeed with
   `No blink(1) devices found.`
4. Render a Linux unit and inspect configuration-only service status.
5. Attempt service installation, status and start without a user manager:
   return a bounded nonzero result, preserve unknown registration, provide
   actionable guidance, and leave no unit or login enable link.
6. Start the actual daemon with an explicitly configured but absent device.
   Check HTTP health, offline device state, pure preview, rejection of an
   offline Job, missing Job lookup, and graceful SIGTERM shutdown.
7. Uninstall the package and confirm the executable link is removed.

The Formula's own test performs help checks and service rendering. It does not
register a service or access USB. The separate container exercise establishes
the empty-device and offline-daemon behavior. Neither is acceptance of a live
systemd user service, Linux USB, login/reboot startup or another platform.

## Upgrades and service paths

Homebrew builds bind the default service executable to `opt/nb1/bin/nb1`,
constrained to the Formula's canonical `Cellar/nb1` directory. Installation and
explicit startup validate ownership, path traversal and executable permissions.
The Formula normalizes the executable mode to 0755, including under umask 002.
On Linux, group-writable directories require a caller-owned private boundary;
shared, writable package paths are not silently trusted.

Upgrading a package does not restart a running daemon. Explicitly stop and start
the service to enter the new version. Removing the old keg must not require
rewriting the service file. Rollback likewise requires installing the chosen
package version and explicitly stopping and starting the service. A restarted
daemon does not recover its in-memory Jobs or deduplication records and does
not replay old light commands.

An explicit `--binary` pins a fixed executable instead of selecting the package
binding. Such a path must remain installed. Configuration changes still require
explicit uninstall/install; no automatic update or restart command is provided.

## Combined package and service acceptance

`check_upgrade_service.py` uses an exact Git archive and a local candidate tap to
exercise real installation, upgrade, old-keg removal, explicit restart, rollback
and cleanup. Its available-manager mode requires a trusted standalone baseline
CLI, runs as the ordinary user and refuses an existing nb1 package or service.
It confirms the kernel-reported executable for every started version and checks
that the service file does not change across upgrade and rollback.

The unavailable-manager mode covers a normal Linux container's foreground daemon
and controlled service errors; it is not a native service lifecycle test. The
focused [Homebrew workflow](../../.github/workflows/homebrew-service.yml) runs
with a real systemd user manager. The standalone daemon checks are described in
[the native acceptance guide](../../ci/README.daemon-service.md). Neither test
qualifies Linux USB or login/reboot behavior.

Before publication, choose real release inputs and a tap repository, and retain
platform-specific acceptance evidence. No tag, release or public tap is created
by the renderer or local candidate checks.

References: [Homebrew on Linux](https://docs.brew.sh/Homebrew-on-Linux),
[Go Formula guidance](https://docs.brew.sh/Language-Specific-Formulae#go),
[Formula tests](https://docs.brew.sh/Formula-Cookbook#add-a-test-to-the-formula),
[Homebrew terminology](https://docs.brew.sh/Manpage#terminology).
