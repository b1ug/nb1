# Try a matching nb1 candidate

[简体中文](candidate-install.zh-CN.md)

This guide covers development candidates, not a tagged release or a published
package channel. Each suite records `release: false`. The first physical-device
delivery target is the tested macOS / Apple Silicon / MK2 / FW204 combination.
An archive for another platform does not establish USB or managed-service
support there. See [platform evidence and limits](daemon-validation.md).

## Verify the suite before running it

Obtain the candidate directory from its agreed delivery location. There is no
public download URL or Homebrew tap for this candidate. Keep these five files
together; `<commit12>` means the first twelve characters of the same core commit:

| File | Contents |
| --- | --- |
| `nb1-<commit12>-<os>-<arch>.tar.gz` | CLI, foreground server and embedded Web; `nb1`, `LICENSE`, `build.json`, `cli-help.json`, `SHA256SUMS`. |
| `nb1-mcp-<commit12>-<os>-<arch>.tar.gz` | Independent stdio adapter; `nb1-mcp`, `LICENSE`, `build.json`, `README.md`, `SHA256SUMS`. |
| `blink1-control-<commit12>.zip` | Complete Skill: `SKILL.md`, `agents/openai.yaml` and three files in `references/`. |
| `suite.json` | Matching source, target and artifact identity. |
| `SHA256SUMS` | Checksums for the suite files. |

The OS names are `darwin`, `linux` and `windows`; architecture names are
`arm64` or `amd64`. Windows binary archives use `.zip` and `.exe`. The Skill is
platform independent. These names describe the packaging contract, not an
availability promise for every target.

On macOS, in that candidate directory:

```sh
shasum -a 256 -c SHA256SUMS
```

Linux can use `sha256sum -c SHA256SUMS`. Check the full source commit, target,
and `release: false` in `suite.json` against the intended delivery. From the
matching source checkout, the offline verifier also checks the suite's archive
contents and identity without executing a binary, accessing USB or starting a
daemon:

```sh
python3 ci/candidate-suite.py verify --directory /absolute/path/to/candidate-directory
```

The verifier is a repository tool, not a file inside the binary packages.
Checksums detect inconsistency; they are not a signature or independent proof
of origin. Replacing a file and its checksum can pass a checksum check. Obtain
the suite and its expected source identity from a trusted delivery source.

## Unpack into your own directory

Each archive has files directly at its root. Use separate, new directories so
that the CLI and MCP manifests do not replace one another. The following
example is for the first macOS arm64 target; replace `<commit12>` in the filename:

```sh
mkdir -m 700 nb1-cli
tar -xzf "nb1-<commit12>-darwin-arm64.tar.gz" -C nb1-cli
cd nb1-cli
shasum -a 256 -c SHA256SUMS
./nb1 --help
./nb1 version
```

Keep this directory at a trusted, stable location if you later register a
service from it. These commands install nothing globally and start no service.
Compare `build.json` with the suite's source and target; the older version
display is an additional diagnostic, not the suite's identity contract.

## Select a device and configuration

Use this candidate's executable explicitly while trying it. `./nb1 list`
enumerates attached devices; it does not choose a default. If none are found,
stop here to check the connection rather than invent a serial.

```sh
./nb1 list
./nb1 config path
./nb1 config init
./nb1 config show
```

`config init` creates a private template and refuses to overwrite an existing
file. Inspect and edit the selected file. On macOS/Linux it defaults to
`~/.config/nb1/config.yaml`; an absolute `XDG_CONFIG_HOME` selects a different
root. Windows uses `%APPDATA%\nb1`. `--config` and `NB1_CONFIG` can select a file.
See [configuration precedence](api-v1.md#user-configuration).

For example, replace `20000001` with the device's actual quoted eight-digit
hexadecimal serial:

```yaml
device: "20000001"
server:
  devices: ["20000001"]
  listen: "127.0.0.1:8931"
jobs:
  terminal_limit: 16384
  terminal_ttl: 30m
library_dir: ""
```

The empty library path keeps local colors and Patterns under the application
configuration root. On macOS, device ownership locks remain at
`~/Library/Application Support/nb1/device-locks`. A different `--config` file
alone moves neither locks nor the default library. Keep the same lock location
for every controller of a device. Existing data is not migrated automatically.

## Choose foreground server or direct playback

To share a device with CLI, Web and MCP clients, start a foreground server:

```sh
./nb1 server --devices DEVICE_SERIAL
```

This same executable is the CLI and the server. It holds the selected device
and listens on loopback; it does not install a login service. In another terminal
in the CLI directory, create `action.json`:

```json
{"action":{"kind":"pulse","color":"lime","cycles":2}}
```

```sh
./nb1 control devices
./nb1 control preview action.json
./nb1 control run DEVICE_SERIAL action.json --wait
```

Open `http://127.0.0.1:8931/demo/` for the embedded workbench. Previewing or
opening the page does not play a Job. HTTP acceptance is not completion; check
the returned Job's terminal status. Stop the foreground server with Ctrl-C.

For one foreground playback without HTTP, explicitly choose direct mode after
the other controller has released that device:

```sh
./nb1 control --direct run DEVICE_SERIAL action.json
```

Direct mode always waits and shares the same ownership lock. It rejects
`--id` and `--endpoint`; its Job exists only for that invocation. An explicit
`--timeout` bounds playback waiting, followed by a separate cleanup wait.
`engine_closed` reports teardown, not successful playback or an optical result.
See [direct lifecycle limits](api-v1.md#cli-connection-policy).

When the daemon is unavailable, ordinary `control` reports an error. The user
chooses to start it or select direct mode. It never starts a daemon or switches
to USB automatically. No keyboard or input-monitoring permission is needed.

## Optional current-user service

macOS uses a LaunchAgent; Linux requires a running systemd user manager. Windows
managed install/start/stop/uninstall are not implemented. A normal Linux
container may have no user manager; use the foreground server there.

For a deliberate macOS/Linux installation, first stop any foreground server
using the same device or port, then run the chosen executable from its stable
location:

```sh
./nb1 service install --devices DEVICE_SERIAL
./nb1 service start
./nb1 service status --json
./nb1 control devices
```

Installation enables login loading but does not request immediate startup
unless `--start` is given. Omitting `--binary` selects the running executable,
not another executable found on PATH. An explicit absolute `--binary` can pin
another trusted executable. A registered or active service does not by itself
prove HTTP or USB readiness.

To stop it while keeping its login configuration, use `service stop`. To remove
the managed service, explicitly allow stopping it:

```sh
./nb1 service uninstall --stop
```

Upgrade and rollback are explicit. For a manually unpacked candidate, keep the
old directory, verify and unpack the chosen next candidate into a different
directory, uninstall the existing managed service with `--stop`, then run the
chosen candidate's `service install --devices DEVICE_SERIAL --start`. Use the
same selected profile and device arguments. Check its service status and HTTP
device state. Roll back by repeating those steps with the retained candidate.
Do not delete a directory still referenced by a service.

Homebrew candidates have a separate stable `opt/nb1/bin/nb1` binding: after
an explicit package upgrade or rollback, stop and start the service to enter
the chosen version. There is no automatic restart, `service restart` command
or `brew services` integration. Changing service devices/listen settings also
requires explicit uninstall/install. See [Homebrew lifecycle details](../packaging/homebrew/README.md).

Local colors and Patterns remain on disk. In-memory Jobs and deduplication
records are lost on restart; old playback does not resume automatically. This
guide does not establish backwards compatibility for arbitrary candidate data.

## Add MCP or the Skill independently

Unpack the matching MCP archive into a separate new directory and verify its
inner `SHA256SUMS`, as for the CLI. Configure an MCP host to launch the absolute
path of `nb1-mcp` with these arguments:

```text
--endpoint http://127.0.0.1:8931 --timeout 10s
```

It speaks the MCP protocol over stdio and calls the existing daemon over HTTP;
it does not launch a CLI subprocess, own USB, or start the daemon. Stdout is
reserved for protocol messages; diagnostics go to stderr. The adapter has its
own `--endpoint` and does not read nb1 YAML. Give the host the actual endpoint
if you changed the port. [MCP tools and recovery](../adapters/mcp/README.md)

For the Skill, return to the candidate directory and unpack into a new folder:

```sh
mkdir -m 700 blink1-control
unzip "blink1-control-<commit12>.zip" -d blink1-control
```

Copy the complete resulting `blink1-control` folder into your project's
`.agents/skills/`, keeping `agents/` and `references/`. Inspect any existing copy
before replacing it. The Skill provides instructions, not an executable,
daemon, MCP registration or additional permissions. See [Skill setup and use](agent-guide.md).
