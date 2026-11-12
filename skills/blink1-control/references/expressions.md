# Expressions and field lookup

Use the running MCP tool schema or installed CLI help first. This compact reference travels with the Skill; read the versioned [API v1 reference](https://github.com/b1ug/nb1/blob/4c2c8d644c0ff428f2367d143879eae035c777a7/docs/api-v1.md) or [OpenAPI](https://github.com/b1ug/nb1/blob/4c2c8d644c0ff428f2367d143879eae035c777a7/docs/api-v1.openapi.json) only for exact ranges or more complex input. The [MCP adapter guide](https://github.com/b1ug/nb1/blob/4c2c8d644c0ff428f2367d143879eae035c777a7/adapters/mcp/README.md) covers setup, transport and result envelopes. These are development-candidate references, not proof of the installed version's capabilities.

## Action, Sequence and request envelope

A CLI input file contains exactly one expression: `action`, `sequence` or `pattern_id`. Serial and Job ID belong to the run command or MCP arguments, not that file. For example, `action.json` contains:

```json
{"action":{"kind":"pulse","color":"lime","cycles":2}}
```

The following port is an example: use the task's actual daemon endpoint on every call.

```sh
nb1 control --endpoint http://127.0.0.1:8931 preview action.json
nb1 control --endpoint http://127.0.0.1:8931 run DEVICE_SERIAL action.json --id j_7K3XJNY9ZM --wait
nb1 control --endpoint http://127.0.0.1:8931 job j_7K3XJNY9ZM
```

Replace the example serial and use a fresh ID for a new task. Preserve that ID if the response is lost. `preview --serial SERIAL` enables a cached capability check; preview does not infer the profile's preferred device.

MCP `run_light` adds the target and optional Job ID around the same expression:

```json
{"serial":"DEVICE_SERIAL","id":"j_7K3XJNY9ZM","action":{"kind":"pulse","color":"lime","cycles":2}}
```

Its result wraps the HTTP response as `endpoint`, `id` and `data`. Inspect `data.status`; a successful MCP call is not Job completion.

## Common Action fields

`kind` is required. Do not add fields belonging to another Action, even with a zero/default value.

| kind | Other required fields | Useful optional fields / omitted behavior |
| --- | --- | --- |
| `set` | `color` | `led` defaults to 0; immediate output, then hold. |
| `off` | None | `led` defaults to 0; black, then hold. Does not cancel another Job. |
| `fade` | `color`, `duration_ms` | `led` defaults to 0; fade to target, then hold. |
| `blink` | `color` | `on_ms:250`, `off_ms:250`. |
| `breathe` | `color` | `period_ms:2000`; smooth symmetric envelope. |
| `pulse` | `color` | `attack_ms:60`, `decay_ms:240`, `rest_ms:700`. |
| `alternate` | Exactly two `colors` | `leds:[1,2]`, `period_ms:2000`. |
| `orbit` | Exactly two `colors` | `leds:[1,2]`, `period_ms:3000`; paired fades, not physical rotation. |
| `rainbow` | None | `period_ms:3000`; seven anchors and return to red. |

The six repeating effects default to `cycles:1` and `on_finish:"off"`; optional `on_finish:"hold"` retains the final output. `set/off/fade` accept neither field. `led` is numeric 0 (all), 1 or 2; `alternate/orbit` instead accept `leds:[1,2]` or `[2,1]`.

Durations use integer milliseconds. `duration_ms`, `attack_ms` and `decay_ms` use positive multiples of 10. There is no unlimited repeat; the compiler enforces executed-Step and duration limits. Omit optional fields for defaults: `null`, unknown fields and generally empty values are rejected.

## Color forms

- Names: `"lime"`, `"rebeccapurple"`; case-insensitive frozen CSS names, excluding `transparent/currentColor`.
- Hex3/6: `"#369"` or `"#336699"`; no alpha or bare hex.
- RGB8: `{"space":"rgb8","r":32,"g":96,"b":128}`; all channels required integers 0–255.
- HSV/HSB: `{"space":"hsv","h_deg":210,"s_pct":75,"v_pct":50}`. All fields are required; hue 0–360, percentages 0–100. HSB uses this exact HSV wire form, not `space:"hsb"` or `h/s/b` keys.
- Local alias: `"@status-ok"`; requires an existing saved color. Resolution returns exact lowercase Hex6 without gamma or optical calibration.

Use `resolve_color` to verify an unfamiliar form without selecting a device. Raw Sequence target colors accept only exact Hex6, not the authoring forms above.

## Exact Sequence example

```json
{"sequence":{"timing":"serial","steps":[{"targets":[{"led":0,"color":"#336699"}],"fade_ms":100,"hold_ms":500}],"on_finish":"off"}}
```

`timing` and nonempty `steps` are required. Serial Steps have one target; `fade_ms` and `hold_ms` default to 0. A target requires numeric `led` and exact Hex6 `color`. `repeat` defaults to 1 and must be positive; `on_finish` defaults to `off`.

Serial nominal time includes `fade_ms + hold_ms + 10` milliseconds per Step for the update allowance. `hold_ms` is not the whole phase duration. Prefer a matching Action for a named effect: `blink` with `on_ms:100`, `off_ms:100`, `cycles:2` compiles to a nominal 400ms; four hand-written Steps with `hold_ms:100` instead total 440ms. Check preview timing before playback. Native I/O and cleanup can add real elapsed time.

Interval Sequences require `initial` with physical LED 1/2 targets. Every Step keeps that target count/order and supplies `interval_ms`, with `fade_ms <= interval_ms`; `hold_ms` is forbidden. Read the full schema for interval ranges instead of guessing.

## Error routing

- `invalid_request`: inspect the schema/input; do not blindly retry unchanged JSON.
- `busy`: another Job owns playback. If the user wants it stopped, cancel that active Job, wait for terminal cleanup, then perform the requested operation.
- `job_conflict`: the retained ID belongs to different input/serial. Keep it for recovery; do not choose a new ID merely to bypass an uncertain prior execution.
- `unavailable` or connection/transport error: preserve endpoint and ID; do not start services or switch to direct USB automatically. An uncertain submission is recovered by querying, not replaying.
- `not_found` for a Job: its record may have expired, been evicted or belonged to an earlier process. It does not prove the operation never happened.

Host Pattern save/read instructions are in [local library workflows](patterns.md); direct execution and configuration are in [direct mode and configuration](direct-and-config.md).
