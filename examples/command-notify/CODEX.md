# Codex turn notifications

[简体中文](CODEX.zh-CN.md) · [Command wrapper](README.md) · [Verification](VERIFICATION.md)

These examples send a finite blue blink through an already running nb1 daemon:
blue for 1.5 seconds, then off in the same Job. Blue means **a turn ended**. The
event does not establish whether the user's task succeeded.

Choose the script for the event source:

| Source | Script | Input | Successful stdout |
| --- | --- | --- | --- |
| Codex external `notify` command | `codex_notify.py` | One JSON argument appended by Codex; `type: "agent-turn-complete"`, `thread-id`, `turn-id`. | Empty. |
| Codex lifecycle Stop hook | `codex_stop.py` | JSON on stdin; `hook_event_name: "Stop"`, `session_id`, `turn_id`, `stop_hook_active: false`. | `{}`. |

Keep the scripts beside their shared `notify.py` helper. The ordinary command
wrapper in [README.md](README.md) takes command argv; it is not either Codex event
receiver. Both event scripts preview by default and require `--execute` before
calling nb1.

## External notify: preview, then opt in for one invocation

The official [notification guide](https://learn.chatgpt.com/docs/config-file/config-advanced#notifications)
documents a command array with one JSON argument supplied by Codex. Preview a
synthetic event without a daemon or hardware:

```sh
python3 examples/command-notify/codex_notify.py \
  '{"type":"agent-turn-complete","thread-id":"demo-thread","turn-id":"demo-turn"}'
```

Preview writes a short diagnostic to stderr, leaves stdout empty, and creates no
report or Job. Unknown, malformed, oversized or incomplete events are ignored.
Supported events require nonempty thread and turn IDs, each at most 256 UTF-8
bytes. The whole JSON argument is limited to 256 KiB.

For execution, prepare Python 3.8+, a matching nb1 executable, a running loopback
daemon with the intended device, and an existing private report directory.
Replace every path, endpoint and serial below. Use absolute executable/script
paths; `DEVICE_SERIAL` must name a device configured by that daemon.

```sh
codex exec \
  --ignore-user-config --ephemeral \
  --skip-git-repo-check --sandbox read-only \
  --cd /absolute/path/to/empty-workdir \
  -c 'notify=["/absolute/path/to/python3","/absolute/path/to/command-notify/codex_notify.py","--execute","--nb1-binary","/absolute/path/to/nb1","--endpoint","http://127.0.0.1:18000","--serial","DEVICE_SERIAL","--report-dir","/absolute/path/to/private-reports"]' \
  'Reply exactly NB1_NOTIFY_DONE. Do not use tools.'
```

Do not append the sample JSON to this array: Codex appends the real event.
Omit `--execute` from the array for preview only. Starting or installing the
daemon is a separate choice; this script does neither and never falls back to
direct USB.

This command changes configuration for that invocation. Project-local
`.codex/config.toml` cannot supply `notify`; see the official
[configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference).
The options above were checked in the installed `codex-cli 0.166.0` public
`exec --help`. `--ignore-user-config` skips the user's `config.toml`;
authentication still uses the existing Codex installation. `--ephemeral` avoids
persisting session files. The read-only sandbox applies to model-generated
commands; the deliberately configured notifier is a host program. This example
does not edit global configuration, change `HOME`/`CODEX_HOME`, or bypass hook
trust. Normal managed policy can still apply. Select a model available to your
installation when needed; the example does not pin one.

## Reports and recovery

External notify execute mode requires `--nb1-binary`, `--endpoint`, `--serial`
and `--report-dir`. The binary must exist and be executable at an absolute path.
The existing report directory must be absolute and cannot itself be a symlink.
On POSIX it must belong to the current user with no group/other permission bits
(for example, mode `0700`); the script refuses an unsuitable directory rather
than changing it. On Windows, keep the directory private using its ACL.
The endpoint must be an explicit loopback HTTP origin with a port. The script
has a fixed four-second submission/wait budget. A timeout does not cancel an
accepted Job.

Reports use `schema_version: 2`, `trigger: "codex_notify"` and
`meaning: "turn_ended"`. They are named `codex-notify-<hash>.json`, using a
SHA-256 hash of thread and turn IDs. Creating the report is exclusive: a repeated event preserves the
existing report and issues no new request. Reports omit message text, cwd,
transcript paths and raw event IDs; they retain the intended endpoint, serial,
Action, original short Job ID and lookup argv. The report directory's lifecycle
belongs to the caller. Keep a report while repeat delivery must be suppressed;
deleting it removes that event's deduplication gate.

Before the single `control run SERIAL FILE --id ID --wait`, the helper saves
`notification.job_id`, `notification.input` and `notification.job_lookup_argv`.
It sends at most one POST. Busy/offline devices, a lost response, invalid CLI
output or a timeout do not cause resubmission, direct fallback or automatic
cancellation. Query the **original Job ID on the original endpoint** using the
saved lookup argv.

`notification.state: "succeeded"` with `final_off: "daemon_job_confirmed"`
requires the CLI to confirm a matching successful terminal Job. An unknown
result remains unknown; a missing Job after expiry, eviction or restart does
not prove the light never ran. A terminal software result is not optical
evidence.

| Adapter result | Exit code | Meaning |
| --- | --- | --- |
| Preview or ignored event | `0` | No playback was requested. |
| Valid event with a notification attempt | `0` | Inspect the report; this code does not certify playback. Operational failures are reported without requesting continuation. |
| Invalid command-line options | `1` | Fix the explicit notifier configuration. |

Event handling keeps stdout empty; `--help` prints the normal command help.
The adapter's exit status is separate from the Codex process's exit status.
Recorded `codex-cli 0.166.0` acceptance confirmed a real successful notification
and a real unavailable-daemon case, both with Codex exit `0`. Recheck delivery
and host failure behavior on the target installation. Evidence is listed in
[VERIFICATION.md](VERIFICATION.md).

## Lifecycle Stop is a separate entry

Preview the existing Stop receiver with stdin JSON:

```sh
printf '%s' '{"hook_event_name":"Stop","session_id":"demo-session","turn_id":"demo-turn","stop_hook_active":false}' \
  | python3 examples/command-notify/codex_stop.py
```

For optional hook setup, review [codex-hooks.example.json](codex-hooks.example.json)
and replace its paths, endpoint and serial. Prepare the private report directory
first. This file is an example outside Codex configuration directories; use the
normal hook review and trust flow if you choose to install it. See the official
[Hooks guide](https://learn.chatgpt.com/docs/hooks).

Stop receives a different event and returns `{}` on successful stdout. Invalid
options return `1`; it avoids exit `2`, which has Stop continuation semantics.
It ignores continuation events and hashes session/turn IDs into
`codex-stop-<hash>.json`. It shares the finite blue Action, four-second budget,
exclusive-report deduplication and original-Job recovery rules described above.
Do not put `codex_stop.py` in the external `notify` command array or pass notify
JSON to it.

Synthetic tests, real external notify delivery, installed Stop hooks and
physical lighting are separate checks. Use the test command in
[README.md](README.md); the historical verification record remains dated to its
original source and scope.

The 2026-11-13 software acceptance passed 23 tests and four real Codex rounds.
The final positive round reached the real Go in-memory executor with one
successful Job ending black; an explicit synthetic duplicate added no POST.
The final unavailable round retained an unknown result without changing the
Codex exit code. The positive notifier completed before CLI exit in that
observed run; this timing is not a general lifecycle guarantee. No physical
device, global hook installation or Windows support was qualified by these
runs. See [the full acceptance record](VERIFICATION.md#real-codex-notify-acceptance--2026-11-13).
