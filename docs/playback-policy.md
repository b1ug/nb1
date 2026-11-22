# Playback policy

Playback policy decides whether the daemon admits a new light Job. It is shared by HTTP, MCP and daemon-backed CLI submissions, including Actions, Sequences, Patterns and Triggers. Preview, status, cancellation and other management requests remain available while playback is suppressed.

`nb1 control --direct run` evaluates the persistent policy loaded from its config for that invocation, but it cannot see or change the daemon's in-memory override/history. Legacy root commands that open USB directly do not use the daemon admission path. Use the managed daemon interfaces when temporary overrides and request history must be shared across clients.

The default is `always`, preserving existing behavior. The daemon reads the persistent policy at startup; restart it after editing `config.yaml`.

## Persistent configuration

```yaml
playback:
  mode: always       # always | weekly | never
  timezone: local    # local or an IANA name such as America/Los_Angeles
  history_limit: 100 # 1–10000 in-memory policy decisions
  weekly: {}
```

`weekly` permits only the listed windows. Unlisted days and times are suppressed. Each weekday has one allow window, using 24-hour `HH:MM` values and a start-inclusive, end-exclusive boundary.
The `local` timezone uses the machine's local zone; an IANA timezone uses Go's embedded timezone database fallback so the same config works on macOS, Linux and Windows without an extra module.

```yaml
playback:
  mode: weekly
  timezone: Asia/Shanghai
  history_limit: 100
  weekly:
    mon: {start: "08:00", end: "20:00"}
    tue: {start: "08:00", end: "20:00"}
    wed: {start: "08:00", end: "20:00"}
    thu: {start: "08:00", end: "20:00"}
    fri: {start: "08:00", end: "20:00"}
```

An overnight window must say so explicitly:

```yaml
playback:
  mode: weekly
  timezone: local
  weekly:
    fri:
      start: "22:00"
      end: "02:00"
      overnight: true
```

This Friday window also allows Saturday from midnight until 02:00. The next Saturday window, if configured, begins independently. Equal start and end times are invalid; use `never` for no scheduled playback or `always` for all-day playback.

## Temporary override

The daemon holds one temporary override at a time:

```sh
nb1 playback status
nb1 playback override suppress --for 1h
nb1 playback override permit --for 15m
nb1 playback override clear
nb1 playback requests --limit 20
```

`suppress` blocks new playback regardless of the persistent mode. `permit` allows it regardless of that mode. A new override replaces the previous one. `clear` returns immediately to the persistent policy. Overrides expire automatically, last at most 720 hours, and disappear when the daemon restarts.

## HTTP and MCP

| Operation | Behavior |
| --- | --- |
| `GET /v1/playback` | Read persistent mode and effective `allowed` / `suppressed` status, reason and active override. |
| `GET /v1/playback/requests?limit=20` | Read newest policy decisions, up to the configured history limit. |
| `POST /v1/playback/override` | Body: `{"mode":"suppress","duration_ms":3600000}` or `{"mode":"permit","duration_ms":900000}`. |
| `DELETE /v1/playback/override` | Clear the temporary override. |

MCP exposes matching `get_playback_status`, `list_playback_requests`, `set_playback_override` and `clear_playback_override` tools. Remote HTTP and MCP use the existing HTTPS Bearer authentication. The loopback API keeps its existing optional token setting.

A rejected playback submission returns HTTP `423` with code `playback_suppressed`. It creates no Job, does not enter a device queue, sends no hardware command and is not replayed automatically after the policy changes. The client can retry as a new request when playback is allowed.

The bounded decision history stores only a timestamp, decision, reason, request kind, target and supplied request/Job ID. It stores no Action, Sequence, Pattern body or user text. It is in memory, newest first, defaults to 100 entries, and is cleared on daemon restart. It records policy decisions, not proof of successful physical light output.

## Boundaries

The `always`, `weekly` and `never` values are Playback modes. This feature does not read macOS Focus, Windows quiet time or Linux desktop notification settings. A request policy decision controls new work only; cancellation, Job lookup and diagnostics remain available. A Job already admitted is not stopped when a weekly window closes or an override changes.
