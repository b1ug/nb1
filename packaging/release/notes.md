## nb1 v0.1.2

This patch packages the public nb1 Embedded Workbench from its own reviewed
source repository. The CLI, optional local daemon and embedded workbench remain
one executable. HTTP, MCP, Action/Sequence semantics and hardware dependencies
are unchanged.

### Fixed

- Remove development-only reference notes, local capture records and workstation
  paths from the embedded Web bundle. Public model notes describe the preview
  without shipping internal research or device-specific acceptance records.
- Bind the bundle to the exact public workbench source commit and complete file
  manifest. Import rejects workstation-specific paths in plain and decoded
  assets; publication checks cover the actual embedded bundle.
- Share the released nb1 v0.1.1 Go compiler source with the browser build,
  including the matching colorkit license. Original approved artwork and model
  geometry bytes are preserved.
- Distinguish the static documentation, embedded HTTP workbench and independent
  WebHID browser tools. Ordinary Go builds use committed, verified Web assets;
  they do not run Node, fetch frontend source or require Git submodules.

### Install and run

Download the matching archive, `release.json` and `SHA256SUMS` from this release.
Verify and unpack it into a new trusted directory, then run:

```sh
./nb1 version --json
./nb1 list
./nb1 server --devices YOUR_DEVICE_SERIAL
```

Open `http://127.0.0.1:8931/demo/`. Windows uses `nb1.exe`. The matching MCP
adapter and Agent Skill are separate optional downloads. Package installation
neither starts a daemon nor registers a service. Existing running instances are
not replaced automatically.

The [Homebrew tap](https://github.com/b1ug/homebrew-tap) installs the version
currently published in its Formula; verify `nb1 version` after installation.
The [English/Chinese documentation](https://b1ug.github.io/nb1-docs/) describes
installation, device ownership and the available APIs.

### Scope

The terminal behavior from v0.1.1 remains: Fang renders human help/errors, Charm
Log renders human stderr diagnostics, and protocol stdout and JSON file logs
remain separate. Internal Zap/hlog interfaces and file rotation remain intact.

Prebuilt CLI/server binaries require macOS 15+ or Linux glibc 2.38+. Optional MCP
binaries require macOS 13+ and are static on Linux; a compatible nb1 server is
still required. Mac downloads are not Developer ID notarized. Source builds
require Go 1.25.8+; release and Homebrew builds use Go 1.27.4+.

Source and module identities, notices, inner file inventories and hashes are in
`release.json`. GID v0.0.3, blink1-go v0.0.2 and colorkit v0.1.0 are unchanged.
This release adds no USB, optical, MK3, login/reboot or device-storage
qualification. Experimental browser/extension tools and future Trigger,
composition, queue/preemption and Windows automatic services remain separate.
