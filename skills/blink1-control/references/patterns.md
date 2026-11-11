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

Preview before saving to inspect the compiled Sequence. `save_pattern` fixes resolved colors and normalized Steps. `list_patterns` returns ID/title summaries; `get_pattern` returns the complete saved Sequence.

```sh
nb1 control preview orbit.json
nb1 control patterns save red-blue-orbit orbit.json --title "Red and blue orbit"
nb1 control patterns list
nb1 control patterns get red-blue-orbit
```

To preview or run a saved Pattern, use `{"pattern_id":"red-blue-orbit"}` with the ordinary preview/run interface. Preserve the Job ID. A matching repeated Job is resolved before the library is reread, including after that Pattern is changed.

Saving an existing Pattern ID replaces its host record atomically. Use a new ID when the user wants both works. After an uncertain save response, read that ID and compare the full Sequence before deciding to save again. A successful preview does not prove storage; playback is not a storage test.

Limits: 256 colors, 128 Patterns, 1 MiB per record and 256 Unicode characters per title. Concurrent writers wait at most two seconds. The daemon selects its library with `--library-dir` or `NB1_LIBRARY_DIR`, otherwise the user's `nb1/library` configuration directory. Returned paths are display data, not file-access instructions.
