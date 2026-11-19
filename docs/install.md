# Install and use nb1 v0.1.3

[简体中文](install.zh-CN.md)

Download the matching executable from [nb1 v0.1.3](https://github.com/b1ug/nb1/releases/tag/v0.1.3). The CLI, local server, MCP and embedded Nightlab workbench are one `nb1` executable. Prebuilt executables need no Go or Node.js installation, and the embedded page needs no browser WebHID permission.

## Choose and verify a download

| Computer | nb1 archive |
| --- | --- |
| Apple Silicon Mac | `nb1-v0.1.3-darwin-arm64.tar.gz` |
| Intel Mac | `nb1-v0.1.3-darwin-amd64.tar.gz` |
| Linux arm64 | `nb1-v0.1.3-linux-arm64.tar.gz` |
| Linux amd64 | `nb1-v0.1.3-linux-amd64.tar.gz` |
| Windows amd64 | `nb1-v0.1.3-windows-amd64.zip` |

The prebuilt executable requires macOS 15.0 or later on Mac and glibc 2.38 or later on Linux. The same requirements apply to `nb1 mcp`. The macOS downloads are not Developer ID notarized.

These binary requirements are separate from source compatibility and do not guarantee every operating system or physical USB combination. See the release's build and platform evidence before using an older system. Physical USB evidence for the recorded macOS arm64 / blink(1) MK2 / FW204 combinations is separate from software checks on other targets. Windows supports foreground operation; automatic service management is not included.

Download the selected archive, `release.json` and the global `SHA256SUMS` from the same release. Compare the archive's complete SHA-256 and exact filename with that manifest. For example, on an Apple Silicon Mac:

```sh
shasum -a 256 nb1-v0.1.3-darwin-arm64.tar.gz
```

Linux can use `sha256sum`; Windows PowerShell can use `Get-FileHash -Algorithm SHA256`. The global manifest also lists other platform archives, so checking every line requires downloading every listed file. Checksums detect inconsistency, not independent origin; obtain the archive and expected manifest from the trusted release page.

Unpack the CLI archive into its own new directory. The archive contains the executable, license notices, a short README, build identity and an inner checksum manifest. On macOS:

```sh
mkdir nb1-v0.1.3
tar -xzf nb1-v0.1.3-darwin-arm64.tar.gz -C nb1-v0.1.3
cd nb1-v0.1.3
shasum -a 256 -c SHA256SUMS
./nb1 version --json
```

Use `tar -xzf` and `sha256sum -c` for a Linux tar archive. Extract the Windows ZIP into a new folder and run `nb1.exe`. Compare `build.json`, the release manifest and `version --json`; they describe the downloaded executable, not another already-running daemon. Keep this directory at a stable location if a service will use it.

## Install with Homebrew

The public [b1ug tap](https://github.com/b1ug/homebrew-tap) installs the version currently published by that tap:

```sh
brew tap b1ug/tap
brew install b1ug/tap/nb1
nb1 version
```

Homebrew builds released source and installs its build dependencies. Its Formula requires macOS 15 or a supported Linux Homebrew environment, with Go 1.27.4 or newer for compilation. The prebuilt Linux binary's glibc requirement above does not describe a Homebrew source build. Check the tap's current version and platform evidence before installing. With Homebrew, use `nb1` in the commands below; `./nb1` selects a manually unpacked executable.

The [documentation site](https://b1ug.github.io/nb1-guide/) includes the English and Chinese setup guides. Installing nb1 does not register or start a background service.

## Open the embedded workbench

Find your device serial, then start the server explicitly:

```sh
./nb1 list
./nb1 server --devices DEVICE_SERIAL
```

Replace `DEVICE_SERIAL` with the serial reported for your lamp. Open **http://127.0.0.1:5885/demo/** in your browser. On upgrade from v0.1.2, existing profiles and installed service definitions that specify port 8931 keep using it. Open the URL printed by your running daemon. Select the intended device, choose an effect or edit a Sequence, then click **Play**. Preview is optional; Play validates the current draft itself. Opening the page, changing a color or playing on screen does not send a Job to the lamp.

The page, local fonts, editor, 3D model and two-face fallback are embedded in nb1. No separate web directory, CDN or frontend build is needed. The default language is English, with Chinese available. If you changed `server.listen` or passed `--listen`, open that actual loopback address followed by `/demo/`.

The browser talks to the same daemon as HTTP CLI and MCP clients. Its HTTP status, selected device state and Job result describe different things: an online device does not mean a draft has played, and accepted is not completed. The page keeps actionable errors and uncertain-Job recovery visible.

Use Ctrl-C to request foreground-server shutdown. Do not start a second owner for the same lamp while the current controller is still cleaning up. A standalone browser WebHID tool owns USB separately; disconnect it before handing the lamp to nb1.

With several configured lights, the Workbench lists them by serial and starts with the first online target. It shows the count and the selected serial. Refresh keeps the selected light. If that serial goes offline or disappears from the configuration, choose another target explicitly to play there.

## Use the CLI or explicit direct mode

In another terminal, save an expression as `action.json`:

```json
{"action":{"kind":"pulse","color":"lime","cycles":3}}
```

```sh
./nb1 control run DEVICE_SERIAL action.json --wait
```

Use `./nb1 control preview action.json` only when you want a plan without playback. CLI results are JSON on stdout; diagnostics go to stderr. If an accepted response is lost, preserve the original Job ID and endpoint and query it instead of submitting new playback. The [API reference](api-v1.md) describes required fields, defaults, errors and recovery.

CLI help and application-generated diagnostics remain English under English, Chinese and other locale settings. The Web workbench has its own language switch. Terminal colors follow the destination stream: any nonempty `NO_COLOR` disables all ANSI styling, including `yes`. `CLICOLOR=0` disables ordinary color; use `CLICOLOR_FORCE=1` or `true` to force color when `NO_COLOR` is unset. Force values follow Go's boolean parsing: `false`, `0` and invalid values such as `2` use ordinary terminal rules. A pipe or `TERM=dumb` otherwise uses plain text. JSON output stays unstyled.

Fang presents human-facing help and errors; application diagnostics on stderr use Charm Log. `--debug` includes caller and PID details. `--log-file PATH` retains the existing JSON file format, 10 MiB rotation, compressed backups and retention policy. Command JSON, the daemon's JSON event stream and MCP JSON-RPC remain separate. CLI translation remains separate work. Building nb1 from source requires Go 1.25.8 or newer, separate from the download and Homebrew build requirements above.

`--log-file PATH` must name an appendable regular file. Directories, leaf symlinks and special files are rejected before the command starts, and an inaccessible or uncreatable target produces a command error. Existing file contents and permissions are preserved. A new target is created during logger initialization, even before the first diagnostic; on Unix, new files and missing parent directories use owner-only permissions. Help and `version --json` do not initialize a log file. File logs contain application diagnostics, not a transcript of Fang errors or daemon events.

Initialization failures are presented once by Fang. A later internal log-write failure is a best-effort Charm diagnostic on stderr, including its original details; it does not retry the command or change a successful device operation into a command failure. If stderr itself is unavailable, diagnostics cannot be guaranteed. File logs keep compressed 10 MiB rotation with unlimited backup retention.

Fang is supplied through a temporary fixed fork of upstream v2.0.1, with the exact public module version and checksum recorded in the release manifests. Its explicit background option is proposed in [official Fang PR #111](https://github.com/charmbracelet/fang/pull/111). The optional-argument ordering correction is proposed separately in [official Fang PR #112](https://github.com/charmbracelet/fang/pull/112); the help keeps `[SERIAL] FILE` in the same order as actual execution.

For one foreground run without a daemon, first release the lamp from the other controller, then explicitly choose direct mode:

```sh
./nb1 control --direct run DEVICE_SERIAL action.json
```

Direct mode always waits and has its own process-local Job. An unavailable HTTP daemon never causes automatic USB fallback. A successful `hold` can leave a color on the lamp; use an explicit Off when you want it extinguished.

## Optional user service

Downloading or unpacking nb1 does not register or start a service. On macOS or Linux with a working user service manager, stop the foreground server and deliberately install from the chosen stable directory:

```sh
./nb1 service install --devices DEVICE_SERIAL
./nb1 service start
./nb1 service status --json
```

Installation arranges possible login loading. Without `--start`, it does not request immediate startup. The running executable is selected by default; `--binary` can explicitly choose another trusted absolute path. A registered/active service does not itself establish HTTP or device readiness.

To stop it while retaining login configuration, use `service stop`. To remove the managed service and explicitly allow stopping it, use `service uninstall --stop`. Windows provides foreground operation and configuration rendering, not automatic install/start/stop/uninstall. A Linux container without a user manager should run `nb1 server` directly.

For a manually unpacked upgrade, retain the old directory, verify and unpack the new version into a different directory, then explicitly uninstall the old managed service with `--stop` and install the chosen executable. Keep the same profile and device selection. Do not delete a directory still referenced by a service. For Homebrew, use `brew upgrade b1ug/tap/nb1`, then explicitly stop/start a managed daemon when ready. The default service binding follows the stable `opt/nb1/bin/nb1` entry; an explicit `--binary` remains fixed. Follow the [tap lifecycle instructions](https://github.com/b1ug/homebrew-tap#upgrade-and-remove). Services are managed with `nb1 service`, not `brew services`.

## Configuration, MCP and the Skill

`./nb1 config path`, `config init` and `config show` locate, create and inspect the selected profile without USB. The profile defaults to `~/.config/nb1/config.yaml` on macOS/Linux, following an absolute `XDG_CONFIG_HOME` when set; Windows uses its native application configuration directory. Local colors and Patterns stay on the computer, independent of device RAM or Flash.

### Connect a local MCP client

Start the daemon, then set the MCP client's executable to the absolute path of the installed `nb1` (`nb1.exe` on Windows). Its first argument must be `mcp`. This example connects to a daemon on the default port.

```json
{
  "mcpServers": {
    "nb1": {"command": "/absolute/path/to/nb1", "args": ["mcp"]}
  }
}
```

**Upgrading from v0.1.2.** Replace the old `nb1-mcp` executable path and prepend `mcp` to its existing argument list. Keep any explicit endpoint. For example, `["mcp", "--endpoint", "http://127.0.0.1:8931", "--timeout", "10s"]` still targets a daemon on the old port. Reload the client and check that it discovers the tools. For a v0.1.2 installation, use the [v0.1.2 guide](https://github.com/b1ug/nb1/blob/v0.1.2/docs/install.md).

For local authentication, add `--token-file PATH`. A client on another computer can connect to the daemon’s HTTPS `/mcp` URL. [Remote setup and local authentication](remote-access.md)

### Add the optional Skill

Unpack `blink1-control-v0.1.3.zip` into a new `blink1-control` folder and copy that folder to the project's `.agents/skills/`. Keep `SKILL.md`, `agents/` and `references/` together. [Agent setup](agent-guide.md)

The release provides the current Action/Sequence, Job and computer-side library contract. Use a named Trigger to play a saved Pattern. The daemon can resolve an omitted serial and records the selected device on the Job. Editable multi-format directories, public queue/preemption and new device-native deployment APIs remain planned. [Trigger configuration and target selection](trigger-routing.md) [Development candidate installation](candidate-install.md) documents CI suites marked `release:false`; those artifacts are not interchangeable with this tagged release contract.
