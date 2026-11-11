# Codex turn notification

`codex_stop.py` reads a Codex `Stop` JSON event from stdin. It previews by default. Execute mode uses the current nb1 Job CLI to hold blue for 1.5 seconds, then finish off in the same finite Job. Blue means a turn ended; the event supplies no task outcome.

The event and output contract was checked against the [official OpenAI Hooks documentation](https://learn.chatgpt.com/docs/hooks#stop) on 2026-11-11 and local `codex-cli 0.166.0` (`codex --version` / `--help`). Stop expects JSON on successful stdout; this script returns `{}`. Invalid options return 1 because exit 2 has continuation semantics. [Official Stop output rules](https://learn.chatgpt.com/docs/hooks#stop)

```sh
printf '%s' '{"hook_event_name":"Stop","session_id":"demo-session","turn_id":"demo-turn","stop_hook_active":false}' \
  | python3 examples/command-notify/codex_stop.py
```

This previews a synthetic event. For optional configuration, review [codex-hooks.example.json](codex-hooks.example.json), replace every local path, endpoint and serial, and create a private report directory first. Quote paths containing spaces for the configured shell. The file is an example outside Codex configuration directories. Installation is a separate user action; use Codex's normal hook review and trust flow. [Official hook configuration and trust](https://learn.chatgpt.com/docs/hooks)

The report filename hashes the session and turn IDs. Repeated events retain the existing report and issue no requests. Continuation events are ignored. Input is bounded to 256 KiB. Assistant text, transcript paths and raw session/turn IDs are not stored. A four-second budget covers the current `control run SERIAL FILE --id ID --wait`; busy devices reject new work.

Before the single POST, the report saves the original `job_id`, Action input and `job_lookup_argv` (`control job ID`). Unknown results are queried by that ID without resubmission. Only a matching successful terminal Job confirms daemon cleanup. Jobs are in memory; a missing Job after retention or restart does not prove that playback never occurred.

Use the test command in [the notification guide](README.zh-CN.md) for the current real nb1 binary and ephemeral synthetic HTTP. Tests feed JSON directly to this script, including duplicates, continuation, malformed input, privacy and lost-response recovery. **Synthetic is not live hook:** no real Codex turn, user configuration change, USB access or device linkage is covered by these tests.
