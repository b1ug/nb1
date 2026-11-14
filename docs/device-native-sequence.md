# Device-native sequence model (internal draft)

The `internal/devicesequence` package implements offline author parsing, placement/playback checks, raw row inspection and playback-register inspection. It does not add HTTP routes, CLI commands, MCP tools, SDK pin changes, a device executor or new artistic actions. `spin`, `swap` and `switch` remain on hold.

High/low describes abstraction, host/device describes execution location, and RAM/Flash describes lifetime. These axes are independent. A native sequence is an explicit limited row language, not an alias for a complete host Sequence.

```json
{
  "contract": "nb1-device-sequence/draft-1",
  "color_space": "device-rgb8",
  "lines": [
    {"led": 0, "color": "red", "duration_ms": 500},
    {"led": 2, "color": "#00f", "duration_ms": 500}
  ]
}
```

Each row has one RGB color, one numeric LED selection (`0`, `1`, or `2`) and one duration shared by firmware fading and row scheduling. It does not imply an instantaneous color switch, independent fade/hold, simultaneous different-color targets, a one-time initial or a final off action. Repeat, memory target and startup are separate concerns.

The `led` field is required. Integer `0` selects all outputs, and `1` or `2` selects the corresponding physical channel, matching Action and Step. Quoted numbers, `"all"`, fractions, exponents, signs, null and values outside `0..2` are rejected. Decoded native sequences use the same numeric form. Unknown raw `led_code` values remain readable observations and cannot be converted into an authorable row.

`CompileJSON` accepts the same bounded ColorInput grammar as the existing color resolver: names, Hex3/6, RGB8 and HSV objects. It freezes normalized Hex6 RGB8 without gamma, retains a separate caller-owned copy of the exact source bytes, and hashes a deterministic normalized sequence. Durations require ordinary integers, positive multiples of 10ms and at most655350ms. That last value is a **wire encoding bound**, not a qualified fader limit. Explicit nulls, duplicate fields, extra fields, fractions/exponents, invalid UTF-8, excessive depth and more than32 rows are rejected.

## Separate pure operations

| Operation | What it establishes | What it does not establish |
| --- | --- | --- |
| `CompileJSON` | Strict author syntax, canonical raw RGB8 and wire-grid timing | Device applicability, capacity, execution or visual effect |
| `PlanPlacementJSON` | Absolute positions, target capacity, row encodings and source/profile digests | A Flash whole-image commit, playback bounds or permission to write |
| `PlanPlayback` | Explicit finite1–255 or forever, selectable range and historic end/count fields | An uploaded image, execution ticket, physical completion or permission to start |
| `InspectSnapshot` | Preserved raw observations and optional exact known-field decoding | Atomic snapshot, high-level recipe recovery, host save, Flash verification or deployment permission |
| `InspectPlayback` | Preserved S prefixes and optional reference-based register range interpretation | An active job, currently lit row, repeat intent, fade settlement or completion |

Uploading line0 is valid. Playing **only** line0 cannot be expressed by the historic `p end=0` encoding, which selects the complete capacity. Placement and playback checks therefore must not be combined into a single boolean. There is no silent relocation, repetition or target fallback.

All absolute ranges stay inside positions0–31. This avoids the documented firmware behavior that aliases out-of-range R/P positions to0 and turns `p end=32` into an invalid internal33. The draft models the known MK2/MK3 row capacity; future larger devices need an explicit contract change.

## Evidence and execution remain separate

Capabilities are a trusted caller/provider argument, not part of the author's JSON. This package does not duplicate the SDK hardware database or accept a user claim of qualification. A future SDK adapter must supply reference capacities, usable Flash range, applicable duration bound and source evidence. Nil facts remain unknown. In particular, physical Flash capacity does not substitute for an unknown usable range.

`encoding_status=exact` only describes normalized row encoding. `capacity_status`, `firmware_applicability` and `operation_qualification` are separate results. Wire-exact rows can still exceed a supplied firmware time bound. There is no qualification registry or executor in this slice: **every plan and inspection has `deployable=false`**, including valid software fixtures. A Flash placement always reports the need for a separate complete affected-image persistence plan. No W/B/F command or HID report is emitted.

The profile digest binds the supplied reference values; it does not authenticate them or identify a genuine connected device. Future admission must additionally bind the real device/firmware, current image, worker ownership and qualification evidence. These preview objects are not admission tickets.

## Raw inspection is not author validation

`Snapshot` contains an explicit requested range, a collector completeness flag and row observations. Each row retains the requested absolute position, buffer layout, whether a response read was attempted, the transport's returned length, exact available response bytes and any read error. The known layout refers to the GID standard report buffer with its ID slot, not the physical USB transaction. The response's LED byte is not a line-position field.

Inspection deep-copies caller data. Raw zero rows, unknown LED codes, special flags, header bytes and unknown tail bytes remain intact. A complete raw observation with an unrepresentable row succeeds as a raw snapshot but has `sequence=null` and specific decoding issues. A partial read, n+error, invalid/mismatched length, unknown layout, duplicate/out-of-range position, missing row or false collector flag cannot become a complete sequence. No zero-filling occurs. Unknown layouts can retain a bounded raw buffer but cannot be decoded as standard R responses.

Only a complete range of representable positive-time rows produces `DeviceSequence`. The digest compares known semantic row fields; it does not claim byte-for-byte roundtrip of R responses and write reports. Decoding does not confer deployability. Rows are ordered by absolute position for the derived sequence, while original observation order is preserved in the snapshot. The device has an array of rows, not32 named patterns; zeros do not identify author-defined boundaries.

The inspection is sequential best effort, not firmware-atomic. Observation timestamps, real device identity, cached/live provenance, error classification and ownership must be provided by the future reader/service envelope. This pure component generates none of them. It never imports into or overwrites the host library. R targets cannot recover titles, source, recipes or revisions, and R is not a Flash reader.

## Playback registers are observations, not execution receipts

`InspectPlayback` accepts a caller-owned S observation and a separate trusted `PlaybackProfile`. It preserves the exact supplied raw buffer and read error, and exposes each raw scalar only when it falls within a valid returned GID prefix. Partial prefixes with an error can still expose available fields, but cannot acquire a normalized range. Invalid or mismatched lengths, an unattempted read or an unknown layout expose no interpreted fields. Raw headers and tail bytes are never rewritten; all exposed data is copied.

The profile must explicitly supply the reviewed S end encoding (`exclusive`, `inclusive`, or `unknown`). No model-name inference takes place. The supplied reference must be firmware-matched and RAM capacity known before the function interprets registers as `configured_range`. The digest binds these reference inputs, not a genuine device or physical qualification. Reviewed FW204/205 return an internal exclusive end; reviewed MK3 returns an inclusive end. **The p command's end=0 sentinel does not apply to an S response.** Unknown modes or impossible ranges remain complete raw observations with interpretation issues, rather than being discarded or silently repaired.

`configured_range` describes the reported register values; it does not identify an active sequence or prove that those rows are currently playing. Raw mode0/1/2/3 and unknown values are not reduced to a boolean. Count0 is retained without inferring forever. FW204's S playpos is the **next scheduling cursor**, not necessarily the currently lit row. Stopped scheduling can precede the last fade settling. The result creates no current-row, repeat, completed-job or visual-completion claim and remains `deployable=false`.

This is a pure software component, not a service envelope. Observation timestamps, real device identity, cached/live provenance, typed transport errors and worker ownership still belong to the future reader/service path. Malformed provider input returns `ErrInvalid`; a collector must retain its original raw/error envelope independently, even when interpretation fails. Startup/Notes schemas and native execution are not included in this package.

## Primary references and remaining physical work

- [Official FW204 source](https://github.com/todbot/blink1/blob/f4bf437fa016b2975675fecc2ad18fd04c955fa8/hardware/firmware_mk2/main.c): RAM32, Flash first16, R/P/p/S and the FW204 shared D range behavior.
- [Official MK3 firmware](https://github.com/todbot/blink1mk3/blob/ccf04e26726a4a41ef9689653ae12baa73dbc456/firmware/firmware-v30x/main.c): RAM32/Flash32, different S end readback, startup collateral persistence and separate Notes.

Version-matched source is not proof that the connected production image is identical. RAM read/write, autonomous playback, timing, stop/ownership, MK3, Flash and power-cycle retention require their own bounded qualifications. No hardware operations are performed by this implementation or its tests.
