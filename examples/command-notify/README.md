# Command notifications

[简体中文](README.zh-CN.md) · [Codex notify and Stop](CODEX.md)

Run an argv command, preserve its exit code, then submit one finite blink(1) notification through the running nb1 daemon. Success uses green, failure red. The default duration is 1.5 seconds; the same Job finishes off.

The default mode prints a preview and runs neither the command nor nb1:

```sh
python3 examples/command-notify/notify.py --serial DEVICE_SERIAL -- go test ./...
```

Explicit execution requires a new report path:

```sh
python3 examples/command-notify/notify.py --execute --serial DEVICE_SERIAL \
  --report notification.json -- go test ./...
```

The report is created before the command runs and never overwrites an earlier run. Before submission it saves the Action, endpoint, serial, a short `j_` Job ID and the exact lookup argv. Reports use private file permissions on Unix. Commands use literal argv without an implicit shell.

The wrapper calls `control run SERIAL FILE --id ID --wait` once. Notification failure does not change the original command's exit code. A missing, damaged or timed-out response is reported as unknown; query the saved Job ID. There is no automatic resubmission or cancellation. A confirmed `succeeded` Job establishes daemon cleanup, not independent optical evidence.

```sh
NB1_NOTIFY_TEST_BINARY=/absolute/path/to/nb1 \
  python3 -m unittest discover -s examples/command-notify -p 'test_*.py' -v
```

The fixture tests use a real CLI and ephemeral synthetic HTTP. They do not access USB, the daily daemon, user libraries or global configuration. See [verification scope](VERIFICATION.md).

For Codex turn events, use [the separate Codex guide](CODEX.md): `codex_notify.py` receives the external `notify` JSON argument, while `codex_stop.py` reads a lifecycle Stop event from stdin. Both preview by default and use blue to mean that a turn ended. The command wrapper above remains `notify.py`; it runs an ordinary command and uses its exit code. Synthetic events, a real Codex turn and physical lighting have separate acceptance evidence.
