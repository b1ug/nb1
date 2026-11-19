Give a passing build its own light. Bind a Trigger such as `build.done` to a saved Pattern and a lamp, then play it with `nb1 control emit build.done --wait`. Change the mapping tomorrow and your build script can keep the same command.

### Two lamps, two independent jobs

When the CLI chooses among several configured lamps, it reports the selected serial. It prefers the first online device in serial order. The Workbench keeps your selected lamp when you press Refresh. For an exact target, supply its serial. If that lamp is busy or offline, nb1 reports what happened on that device.

Use `nb1 control triggers --check` to catch a missing Pattern or an unmanaged device binding before playback. Each accepted Job records its serial. If the reply gets lost, look up the original Job ID. A later Trigger edit applies to new Jobs. When cancelling a Job, you can supply its recorded serial to guard the request. [Trigger and device selection guide](https://github.com/b1ug/nb1/blob/v0.1.3/docs/trigger-routing.md)

### One executable, local or across the room

The CLI, daemon, embedded debugger and local MCP server now ship in the same executable. For local agents, run `nb1 mcp` over stdio. For an agent on another computer, explicitly enable the HTTPS listener and connect to `/mcp` with a Bearer token. Both transports offer the same 12 tools. Remote access is off by default. [Remote setup](https://github.com/b1ug/nb1/blob/v0.1.3/docs/remote-access.md)

### Upgrading from v0.1.2

- **Port.** The default moves from `8931` to `5885`. The local API and debugger share that port. Open <http://127.0.0.1:5885/demo/>. Explicit port settings still take priority. When moving a pinned installation, update the daemon, its clients and your bookmark together.
- **MCP.** In your agent configuration, change the executable from `nb1-mcp` to `nb1` (`nb1.exe` on Windows) and put `mcp` first in the argument list. Keep any explicit endpoint or timeout arguments. The Agent Skill remains a separate ZIP.
- **Downloads.** Choose the archive for your platform and check it against `SHA256SUMS`. The prebuilt executable requires macOS 15+ or Linux glibc 2.38+ on those platforms. Windows x64 is available as a ZIP. Source builds require Go 1.25.8+.

This release uses blink1-go v0.0.3, including its Flash capacity profile for MK2 firmware 204. [Installation guide](https://github.com/b1ug/nb1/blob/v0.1.3/docs/install.md) · [Changes since v0.1.2](https://github.com/b1ug/nb1/compare/v0.1.2...v0.1.3)
