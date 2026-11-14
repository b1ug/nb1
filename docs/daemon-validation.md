# Daemon operation and platform limits

`nb1` contains both the CLI and the daemon. Run `nb1 server --devices SERIAL`
in the foreground. Installing or unpacking the executable does not install or
start a service. On macOS and Linux, `nb1 service install --devices SERIAL`
installs a current-user service. Add `--start` or call `service start` to start it.
The running executable is selected by default; `--binary /absolute/path/to/nb1`
selects another verified executable. Windows automatic service management is
not implemented.

`nb1 control ...` uses the selected loopback HTTP endpoint. An unavailable daemon
produces an explicit error. It does not start a daemon or switch to USB.
`nb1 control --direct run SERIAL FILE` runs a foreground Job through the same
core and native device ownership lock. Direct mode always waits and does not
create a daemon record. See [API v1 connection policy](api-v1.md#cli-connection-policy).

Use `nb1 version --json` to inspect this executable and `nb1 doctor` for a
read-only connection and configuration report. Check the actual endpoint and
selected devices before choosing foreground or managed service operation.
[Candidate installation](candidate-install.md) describes matching CLI, MCP and
Skill archives, service setup, upgrades and rollback.

## Software verification

The native CI workflow builds the CLI, runs software tests with the race detector,
and runs `go vet` on supported native runners. The candidate workflow builds the
matching MCP binary, checks stdio discovery, verifies archives and source identity,
and exercises a foreground daemon configured with an absent device. The Linux
user-service workflow uses an isolated fixture and records its own environment.
These checks do not install a daily service or qualify physical USB operation.

The native device backend is selected at build time. Software tests, CGO-disabled
builds, process smoke checks and service-manager fixtures establish different
boundaries. Physical USB, device-generation support, optical behavior, Flash
persistence and actual installation or reboot behavior require evidence for the
exact executable and environment. Historical local-machine validation is kept
separately from the public source contract.
