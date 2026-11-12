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

## External notify follow-up — 2026-11-13

The dated fourteen-test record above remains the 2026-11-11 synthetic validation;
it does not include the new `codex_notify.py` external-notification adapter.
That adapter receives one JSON argument from Codex, while `codex_stop.py` keeps
its stdin Stop event contract. See the [English](CODEX.md) or
[Chinese](CODEX.zh-CN.md) setup guide.

The new adapter tests passed **9 tests, 0 failures, 0 skips**, in 2.556 seconds
on the current macOS host:

```sh
NB1_NOTIFY_TEST_BINARY=/absolute/path/to/nb1 \
  python3 -B -m unittest discover -s examples/command-notify -p test_codex_notify.py -v
```

The supplied frozen CLI is retained in the parent workspace as
`artifacts/codex-notify-live-20261113/bin/nb1`, SHA-256
`5d9e98158ae912ffa2b69368da06b1f7f909b2f9e785614f2962740b1463325e`.
It was built from the provided source archive at
`a7f025c91aa6acb160e0b7fd8efc7c59188e5fbb`. Its actual Go build info reports
Go 1.27.3, `darwin/arm64` and `CGO_ENABLED=0`; it has no embedded VCS revision.
This is an HTTP-client integration artifact, not a native USB release build.

These nine tests cover argv input without reading open stdin, preview and
ignored events, JSON/ID byte limits, sanitized option errors, private report
directory checks, no CLI launch before the original Job ID is durable, real
CLI success/unknown responses and recovery by the original ID, report privacy,
duplicate delivery, six-process concurrent deduplication, and no POST when
the receipt directory cannot be written. Their HTTP server is synthetic; it
does not run the real nb1 execution engine or access USB.

At the helper-test stage, real `codex exec` delivery was still pending. The
following acceptance separately records the later real-turn exercise; the
helper tests above remain synthetic event/HTTP evidence.

## Real Codex notify acceptance — 2026-11-13

The complete notification suite passed **23 tests, 0 failures, 0 skips**, in
9.173 seconds: the existing fourteen command/Stop tests plus nine new notify
tests. It used the same frozen CLI identified above and a temporary empty
`NB1_CONFIG`. The result is retained as
`artifacts/codex-notify-live-20261113/unit-test-summary.json` in the parent
workspace. This test-suite result is separate from the real Codex rounds below.

There were **four actual `codex exec` rounds** with `codex-cli 0.166.0`, requested
model `gpt-6-luna` and high reasoning: an initial positive/unavailable pair,
then both cases again after making the acceptance recorder retain only
sanitized summaries. All four Codex processes exited `0` and made no model
tool calls. The initial pair's raw streams were removed; its sanitized evidence
remains under `initial-recording/`.

The final evidence is in the parent workspace:

| Case | Evidence | Observed result |
| --- | --- | --- |
| Positive | `artifacts/codex-notify-live-20261113/positive/result.json` | Real Codex called a transparent argv observer once; it forwarded that event to the production adapter, frozen CLI and actual Go in-memory execution engine. Exactly one new Job succeeded, both cached LED values finished black, and the original Job ID remained queryable. |
| Unavailable | `artifacts/codex-notify-live-20261113/unavailable/result.json` | Real Codex directly called the production adapter with an exclusively held, non-listening loopback endpoint. The notification stayed `unknown`, retained `outcome_unknown: true`, its original Job ID and lookup argv; Codex exited `0`, and the software fixture's Job count did not increase. |

The tested adapter's SHA-256 is
`7d801e1bb7f23663c92426d8f62c8864dd2b1e864b00500b60a0043552342c91`.
The positive Action used blue `#365f9f`, `on_ms: 1500`, `off_ms: 10`, one cycle
and `on_finish: "off"`. Its recorded Job ran for 1.514102 seconds; the whole
Codex command took 8.702 seconds. The observer recorded
`notifier_completed_before_cli_exit: true`. That is one observed timing result
on this installation, not a guarantee for every Codex version or lifecycle.
The final unavailable command took 9.596 seconds, including the notifier's
four-second budget.

After the positive real callback, the observer explicitly supplied the same
event once more as a **synthetic duplicate check**. The report hash did not
change and no extra POST occurred; `actual_callback_count` remained one. This
does not claim that Codex itself delivered a duplicate event. Across all four
rounds the fixture accepted two Jobs, one per positive case. The fixture was
explicitly shut down; `shutdown.json` records the successful response and final
two-Job count.

The recorder used bounded in-memory captures (2 MiB stdout, 64 KiB stderr).
Final records contain counts, byte totals and a final-response match boolean;
they do not retain raw Codex streams, raw thread/turn IDs or message text.
The runs used a one-invocation `-c notify`, `--ignore-user-config`, `--ephemeral`
and a read-only model sandbox. Existing auth was left untouched. No global
configuration, USB, camera, or daily services at ports 8931/4174 were operated.

This qualifies the recorded macOS software notification path and unavailable
daemon behavior. It does not qualify physical lighting, Windows, installed
Stop hooks, every failure lifecycle, or hard interruption of native I/O.
