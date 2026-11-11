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
uses the executable running the install command. macOS uses the current user's
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
no host mounts or USB passthrough, and disconnects its external network before
runtime checks. These are the checks:

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

## Upgrades and remaining release work

Service installation currently resolves symlinks and stores the real binary
path. A Homebrew-installed service can therefore point at a specific Cellar
version. A stable service path or explicit rebinding workflow remains release
work; installing a new package alone does not prove that an existing service
will survive removal of its old Cellar directory. This upgrade lifecycle has
not been accepted by the container check above.

Before publication, choose real release inputs and a tap repository, perform
native macOS installation and upgrade checks, and test the Linux service on a
system with a running ordinary-user manager. No tag, release or public tap is
created by the renderer or local container validation.

References: [Homebrew on Linux](https://docs.brew.sh/Homebrew-on-Linux),
[Go Formula guidance](https://docs.brew.sh/Language-Specific-Formulae#go),
[Formula tests](https://docs.brew.sh/Formula-Cookbook#add-a-test-to-the-formula),
[Homebrew terminology](https://docs.brew.sh/Manpage#terminology).
