# Notification API v1 software verification

Verified on 2026-11-11, based on nb1 `7151076914d5a4111a2d8954d5b7e13b86c3102f`.

```sh
NB1_NOTIFY_TEST_BINARY=/absolute/path/to/current/nb1 \
  PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover \
  -s examples/command-notify -p 'test_*.py' -v
```

Result: **14 passed, 0 failed, 0 skipped**, in 6.775 seconds on the current macOS host. This includes nine subprocess-mock tests, three current-real-CLI/HTTP test methods with multiple success/failure/timeout/busy/offline cases, and two synthetic Stop test methods. The real-CLI methods and Stop playback cases use a Python synthetic HTTP server on an ephemeral loopback port. The fixture accepts current `POST /v1/jobs` input and serves `GET /v1/jobs/ID`; it has no USB transport.

The supplied integration binary was `artifacts/api-v1-integration-20261111/nb1` in the parent workspace, SHA-256 `10cb090a0bb77bb284d6c6b960fd44b09dcba01b933345e02e91495e9f28392b`. Its embedded build metadata records revision `7151076` and `vcs.modified=true`; this is the parent's supplied integration artifact, not a separately claimed clean build. The complete log, build metadata and machine-readable result are retained at `artifacts/notify-v1-migration-20261111/` in that workspace.

Verified behavior: original command argv and exit code, a durable Job ID before the single submission, a finite blink Action ending off, successful/failed/cancelled Job results, lost-response recovery through the original Job ID, no replay after timeout/busy/offline/invalid responses, no submission if the report cannot be saved, and unchanged reports for duplicate synthetic Stop events. Stop continuation, bounded input, privacy, default preview and JSON stdout behavior are covered.

**Synthetic is not live hook.** No real Codex turn was started, no hook was installed or trusted, no user configuration was changed, and no USB, camera or daily daemon at port 8931 was contacted. This does not qualify physical lighting or other operating systems.

The existing Stop event/configuration shape was checked using local `codex-cli 0.166.0` (`--version`, `--help`) and the [official OpenAI Hooks documentation](https://learn.chatgpt.com/docs/hooks#stop). See [CODEX.md](CODEX.md) for the example's exact scope.
