# Local colors and Patterns

The daemon stores these records on the computer. Saving does not play a Job or write device RAM, Flash or startup settings. MK2 can use the same host library as MK3.

## Named colors

Use `list_colors` and `save_color`, or:

```sh
nb1 control colors set status-ok lime
nb1 control colors list
```

Reference the value as `"@status-ok"` in an Action. Names contain 1–32 lowercase letters, digits or hyphens and start with a letter. Saved values are exact RGB. An alias change affects new previews and Jobs, not an accepted Job.

## Patterns

An author input contains exactly one Action or Sequence:

```json
{"action":{"kind":"orbit","colors":["red","blue"],"period_ms":3000,"cycles":1,"on_finish":"off"}}
```

Preview is optional when inspecting the compiled Sequence. `save_pattern` validates and fixes resolved colors and normalized Steps without requiring a prior preview. `list_patterns` returns ID/title summaries; `get_pattern` returns the complete saved Sequence.

```sh
nb1 control patterns save red-blue-orbit orbit.json --title "Red and blue orbit"
nb1 control patterns list
nb1 control patterns get red-blue-orbit
```

Use `nb1 control preview orbit.json` separately when a preview is wanted.

To preview or run a saved Pattern, use `{"pattern_id":"red-blue-orbit"}` with the ordinary preview/run interface. Preserve the Job ID. A matching repeated Job is resolved before the library is reread, including after that Pattern is changed.

Saving an existing Pattern ID replaces its host record atomically. Use a new ID when the user wants both works. After an uncertain save response, read that ID and compare the full Sequence before deciding to save again. A successful preview does not prove storage; playback is not a storage test.

Limits: 256 colors, 128 Patterns, 1 MiB per record and 256 Unicode characters per title. Concurrent writers wait at most two seconds. The daemon selects its library from `--library-dir`, `NB1_LIBRARY_DIR`, profile `library_dir`, then the stable per-user `nb1/library` default, in that order. A custom profile path alone does not relocate it. Returned paths are display data, not file-access instructions.

Use the actual daemon endpoint for every CLI call; add `control --endpoint URL` when it differs from the selected profile. For exact input fields and CLI/MCP envelope examples, read [expressions](expressions.md); for profile and direct-mode behavior, read [direct mode and configuration](direct-and-config.md).

## Triggers

On a daemon supporting routed playback, a Trigger references a saved host Pattern. It can have a serial binding. List configured names with `nb1 control triggers`. If the user asks to invoke one, submit the exact name through `run_light` or `nb1 control emit NAME --wait`:

```json
{"id":"build-42","trigger":"build.done"}
```

Use one selector per submission. An explicit serial takes precedence over a Trigger binding. With neither, the daemon uses its configured default or sole managed device. Only an administrator's opt-in fallback can substitute the sole online alternative for an unavailable implicit default. Report the actual accepted serial and any fallback. For `target_required`, select a device with the user.

A mapping change applies to new Jobs. Keep the original request and Job ID when checking or recovering an accepted result. Titles are display text. To edit a mapping when requested, see the matching daemon's `triggers.json` configuration guide.

When diagnosing a mapping, use `nb1 control triggers --check` on a daemon supporting trigger checks. The read-only report shows missing Pattern references, unmanaged device bindings and cached connection states.

### Device-guarded cancellation

For a light-specific cancellation, send `id` and `serial` to a compatible `cancel_job` tool. Use the accepted Job's serial to match the user's intended light. A `job_target_mismatch` rejection leaves the Job unchanged. If the daemon rejects the guard, preserve it on retries.
