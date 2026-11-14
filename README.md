# nb1

new blink(1) cli tool

## Continuous integration

The native CI matrix builds the CLI, runs the software tests with the race
detector, and runs `go vet` on Ubuntu 24.04 (amd64/arm64), macOS 15
(arm64/Intel), and Windows Server 2022 (amd64), using Go 1.26.x and 1.27.x.
The declared Go 1.18 source floor is checked separately with Go 1.18.10 and
CGO disabled on Ubuntu 22.04. This source check does not claim native USB
support for that toolchain or every older operating system.

CI keeps the committed module versions with `GOWORK=off` and `-mod=readonly`.
It does not run `make preview`, list USB devices, or play a light effect. The
software regression tests cover output-file handling and reject invalid LED
selections before device access. Passing CI is not physical hardware or mk3
qualification.

Each Go 1.27 native job retains a development CLI, `go version -m` output,
the checked-out source identity, and SHA256 checksums for 14 days. These are
CI artifacts, not tagged release downloads or the separately validated
daemon/MCP/Skill candidate suite. No Release, tap Formula, or installation is
created. Actions are pinned to release commits, checkout credentials are not
persisted, and repository permissions are read-only.
