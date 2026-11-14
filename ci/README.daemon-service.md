# Daemon user-service acceptance

## Native foreground acceptance

`build.yml` builds source-bound CGO candidates on native Linux amd64/arm64,
macOS Intel/Apple Silicon, and Windows amd64 runners. After validating the
candidate package it runs `ci/daemon-process-smoke.py` against that exact
executable, not a mock HTTP server. Its separate evidence artifact records:

- Metadata-only discovery with no blink(1) attached on hosted runners.
- An explicitly absent configured device, HTTP health, CLI access, pure preview,
  rejected offline execution, and the embedded Web assets and policy.
- A refused conflicting listener and foreign Origin, with the original daemon
  still healthy.
- Color and frozen Pattern persistence after process restart from another
  explicit executable path, including paths containing spaces.
- Targeted SIGTERM (POSIX) or CTRL_BREAK (Windows), normal process exit and
  closed HTTP listener; a failed CLI call leaves the daemon stopped.

Run locally with `python3 -B ci/daemon-process-smoke.py --binary /absolute/nb1
--output /new/private/evidence-directory`. Add `--require-no-devices` in an
environment expected to have no blink(1). The script never selects discovered
devices or installs a system service. Each child and HTTP wait is bounded;
forced cleanup counts as failure. Missing best-effort shutdown log records are
reported as unobserved and do not prove engine cleanup.

This qualifies foreground operation only. Windows automatic service installation
is not implemented; managed upgrade, login/reboot and physical USB behavior
require their own evidence. Candidate artifacts are validation outputs, not
releases or published Homebrew packages.

## Linux managed service acceptance

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
