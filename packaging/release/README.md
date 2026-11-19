# Versioned native releases

The release workflow builds the same source into five native targets:
`darwin/arm64`, `darwin/amd64`, `linux/arm64`, `linux/amd64` and
`windows/amd64`. The CLI uses CGO; the matching standalone MCP executable does
not. Both use the fixed Go 1.27.4 build toolchain. The source floor is
Go 1.25.8 for both the root and standalone MCP modules; it is a different
contract from the release-build toolchain.

Human CLI help and errors use a temporary fixed Fang fork based on upstream
v2.0.1; application console diagnostics use Charm Log v2.0.1 on stderr. This
pair avoids mixing incompatible ANSI and Lip Gloss APIs. Log requires
Go 1.25.8; this nb1 choice does not change the foundation libraries' own Go
requirements. English CLI messages, command JSON, MCP JSON-RPC and the daemon's
JSON event writer keep their existing boundaries. Application file logs retain
their JSON encoder, metadata, 10 MiB rotation, compression and backup policy.

The Fang dependency is the ordinary public module `github.com/vt128/fang/v2`
at `v2.0.2-0.20261117154700-ea7a74892849`, whose commit is
[ea7a7489284927655a069bd4da792c25a0259450](https://github.com/vt128/fang/commit/ea7a7489284927655a069bd4da792c25a0259450).
It backports `WithDarkBackground(bool)` to official v2.0.1; the separate
upstream-main proposal is [Fang PR #111](https://github.com/charmbracelet/fang/pull/111).
The opt-in option permits a known background without automatic detection;
its default remains upstream's behavior. The fork also preserves custom optional
argument positions, so `run [SERIAL] FILE` matches the actual command order;
that correction is proposed independently in [Fang PR #112](https://github.com/charmbracelet/fang/pull/112).
Command definitions, argument parsing and the reserved usage conventions remain
unchanged. No Fang replacement or release provenance exception is used. Keep the exact public version, sum and original
license notices in the normal module and archive records.

The CLI already contains the complete Web bundle. There is no Node dependency,
separate Web extraction, CDN fetch or post-install asset download.

## Rehearse before tagging

Relevant packaging and MCP pull requests automatically run a read-only
rehearsal. After the workflow has landed on the default branch, run a manual
rehearsal of that exact source:

```sh
gh workflow run release.yml --ref master -f version=v0.1.3 -f publish=false
```

The tag workflow publishes the text from `packaging/release/notes.md`. For v0.1.3, check that both archive READMEs and the tag notes identify port 5885 and that the notes explain how to keep an explicitly configured 8931.

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
stable SemVer tag, such as `v0.1.3`. The official `b1ug/nb1` tag must peel to the
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
- One platform-independent `blink1-control-vVERSION.zip`, containing the five
  Skill instruction/reference files and the original root LICENSE.
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
requires Go 1.27.4 to statically re-read the exact archived executable bytes
with `go version -m`, including foreign platforms; it never executes them.
The actual build information must equal the manifest and satisfy the expected
source, target, module and toolchain contracts. Platform keys must match five
distinct actual targets, with the complete required target set. It uses the
existing candidate suite's safe archive parser, but the release schema
and versioned filenames are separate. Candidate evidence retains `release:false`;
it is not relabeled as a release or silently promoted.

## Runtime and qualification boundaries

The extracted CLI help/version and MCP initialize/discovery checks use only
the OS system PATH. Linux records actual ELF/glibc requirements (or a genuinely
static executable); macOS records Mach-O minimum OS, system dylibs and actual
signature inspection; Windows rejects dependencies outside the system DLL
contracts. This prevents accidentally relying on the build runner's MinGW or
other private runtime directories.

Windows DLL names are read directly from the bounded AMD64 PE32+ import
directory, independent of objdump's platform-specific text and without reading
exported names as dependencies. Malformed/unmapped/truncated tables and
delay-import tables are rejected; the system-DLL and system-PATH checks remain.

These records are not a claim of Developer ID notarization, Windows publisher
signing, every old OS, real USB on every platform, MK3, managed Windows service,
login/reboot or optical qualification. Physical evidence stays separately
identified. No package installation, service registration or device selection
is performed automatically.

The public [Homebrew tap](https://github.com/b1ug/homebrew-tap) uses a real
versioned source archive after its Release exists. Prepare and verify that
version separately before updating the tap; publishing executable downloads
does not silently advance its Formula. See [Homebrew packaging](../homebrew/README.md).
The [English and Chinese documentation site](https://b1ug.github.io/nb1-guide/)
also has a separate, source-bound publishing workflow.
