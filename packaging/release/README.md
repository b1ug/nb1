# Versioned native releases

The release workflow builds the same source into five native targets:
`darwin/arm64`, `darwin/amd64`, `linux/arm64`, `linux/amd64` and
`windows/amd64`. The CLI uses CGO; the matching standalone MCP executable does
not. Both use the fixed Go 1.27.4 build toolchain. The source floor remains
Go 1.25.0; it is a different contract from the release-build toolchain.

The CLI already contains the complete Web bundle. There is no Node dependency,
separate Web extraction, CDN fetch or post-install asset download.

## Rehearse before tagging

Relevant packaging and MCP pull requests automatically run a read-only
rehearsal. After the workflow has landed on the default branch, run a manual
rehearsal of that exact source:

```sh
gh workflow run release.yml --ref master -f version=v0.1.0 -f publish=false
```

Inspect the run, five native package jobs, assembly and attached evidence.
Each platform records source and Web identity, actual Go build information,
linked public modules and checksums, inspected native dependencies, and
extracted-archive CLI/MCP smoke results. The separate no-device process check
also checks the embedded HTTP assets and foreground daemon lifecycle.

Go may omit `-ldflags` from `go version -m` when `-trimpath` is used. The release
version is therefore also checked by executing the exact extracted CLI's
`version --json` and the MCP binary's real stdio `initialize` response. A version
string in the manifest alone does not satisfy the native smoke gate.

Both `pull_request` and `workflow_dispatch` are always rehearsals. Requesting
manual `publish=true` is an error. Their `build_context` records publication as
ineligible; the publish job cannot run and has no branch/dry-run publication
path. A rehearsal archive is not a public release, even though its executable
uses the intended version for the version/installation checks.

## Publish only a real tag

After reviewing the final source, test evidence and release notes, push its
stable SemVer tag, such as `v0.1.0`. The official `b1ug/nb1` tag must peel to the
checked source and that commit must already be an ancestor of `origin/master`.
Branch pushes, fork tags and malformed tags cannot pass the context gate.

Five successful native jobs are required before assembly. Only the publish
job has `contents: write`; build jobs retain read-only contents permission.
Assembly downloads only the current workflow run's five named platform
artifacts, rejects missing/duplicate/mixed sources, and verifies each inner
archive before producing the global closed inventory.

Publication creates a **new draft**, uploads the entire verified set, downloads
it again and compares every byte before making it public. An existing release
is never overwritten or clobbered. A failed publication may leave a draft for
explicit inspection/recovery; it does not deliberately expose a partial public
asset set. Published tags and checksums are immutable.

## Assets and verification

- Five `nb1-vVERSION-OS-ARCH` archives: executable, embedded Web, short README,
  license files, build manifest and internal checksums.
- Five optional `nb1-mcp-vVERSION-OS-ARCH` archives.
- One platform-independent `blink1-control-vVERSION.zip`.
- `release.json` with all source, target, component, runtime and asset identities.
- Global `SHA256SUMS`, including `release.json` and all eleven archives.

Unix binaries use `.tar.gz`; Windows uses `.zip` and `.exe`. CLI and MCP archives
are unpacked into separate directories because each has its own `build.json`
and internal `SHA256SUMS`. For a single selected archive, compare its complete
outer hash against the global list, then verify the inner list after extraction.
Do not run `-c` against the global list when the other platform archives were
not downloaded.

Each component includes the original top-level `LICENSE*`, `COPYING*` and
`NOTICE*` files from its actual linked Go module selection, plus the Go
toolchain's LICENSE. Source versions/sums and each notice's original-byte hash
are recorded in `build.json`. This includes both GID v0.0.3 `LICENSE` and
`LICENSE-old`, and the MCP SDK's own LICENSE. The CLI also retains the bundled
Web/model/editor/font notices. Missing module notices are reported as an error,
not silently skipped; the packager copies texts without assigning license terms.

`ci/release-suite.py verify` is an offline, bounded, closed-inventory check. It
uses the existing candidate suite's safe archive parser, but the release schema
and versioned filenames are separate. Candidate evidence retains `release:false`;
it is not relabeled as a release or silently promoted.

## Runtime and qualification boundaries

The extracted CLI help/version and MCP initialize/discovery checks use only
the OS system PATH. Linux records actual ELF/glibc requirements (or a genuinely
static executable); macOS records Mach-O minimum OS, system dylibs and actual
signature inspection; Windows rejects dependencies outside the system DLL
contracts. This prevents accidentally relying on the build runner's MinGW or
other private runtime directories.

These records are not a claim of Developer ID notarization, Windows publisher
signing, every old OS, real USB on every platform, MK3, managed Windows service,
login/reboot or optical qualification. Physical evidence stays separately
identified. No package installation, service registration or device selection
is performed automatically.

Homebrew uses a real versioned source archive only after this Release exists.
See [Homebrew packaging](../homebrew/README.md); the tap and source Formula are
separate from these executable downloads and do not block the first download
release.
