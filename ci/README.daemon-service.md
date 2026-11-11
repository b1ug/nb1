# Daemon user-service acceptance

`daemon-service.yml` runs a focused Ubuntu 24.04 job on the
`validation/daemon-service-20261111` branch or by workflow dispatch. It uses the
guarded `systemd-user-smoke.py` tool, a freshly created ordinary user and that
user's real systemd manager. It does not modify the runner's own user service.

The workflow builds the actual CLI/server with CGO enabled and a separate
stdlib-only fixture with CGO disabled. Both use a fixed Go version; build
metadata checks the public blink1-go/GID versions and rejects replacements.

The fixture phase verifies installation without immediate startup, explicit
start/stop/restart, idempotence, exact configuration retention, process exit,
kernel `NoNewPrivs`, and uninstall. Six refusal cases cover an additional
enable link, a drop-in, a mask, a transient unit, a pending reload and bus loss.

The explicitly selected `--actual-daemon` phase then:

- Requires native discovery to find no blink(1) devices.
- Installs the actual CLI as a service without specifying `--binary`, checking
  that it resolves its current executable.
- Verifies the real process executable, ordinary UID, kernel `NoNewPrivs`, HTTP
  health and an offline configured device.
- Runs pure preview, rejects an offline Job, and calls the daemon from the CLI.
- Stops the daemon, checks normal process exit and retained configuration, and
  verifies that a failed CLI call does not start it again.
- Explicitly restarts and uninstalls it, checking the new process identity and
  absence of the unit, enable link and process afterward.

The actual-daemon phase is opt-in; the original fixture-only workflow remains
usable without it. A failure in either phase or cleanup prevents PASS.
Environment unavailability and unexecuted phases never count as success.

Account setup and cleanup use host privileges only inside the isolated runner.
Product commands and user-manager operations run as the fresh ordinary UID
with a clean environment. Evidence excludes environment dumps, credentials,
the account HOME and journal contents. A timed-out account creation remains a
failed attempt even if partial state was later removed.

This qualifies service management and offline daemon behavior on that runner.
It does not qualify Linux USB, udev access, login/reboot startup, a Homebrew
upgrade or another distribution. Native discovery may run; no real device is
selected or sent LED commands. macOS LaunchAgent acceptance is recorded
separately, and Windows lifecycle support is still separate work.
