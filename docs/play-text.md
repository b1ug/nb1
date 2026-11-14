# Play Text files and direct commands

Play Text is a finite, line-oriented format for the existing `execute`, `write`,
and `convert` commands. It describes SDK states, not nb1's higher-level Action
or Sequence language. Each line is metadata, one complete state, a blank line,
or an explicit `//` comment.

```text
title: Desk light 夜灯
repeat: 2
set rgb(1, 2, 3) on led 1
fade to #abc over 500ms on led 2
off
```

`title:` is optional. It preserves Unicode and case. Exactly one optional space
or tab immediately after the colon is a separator; all remaining title text,
including surrounding spaces and `//`, is literal. `repeat:` defaults to `1`
and accepts a decimal integer from `1` through `255`, or `forever` for the SDK's
zero/infinite value. Numeric `0` is rejected in Play Text to make infinite
playback explicit. Metadata keys and `forever` are ASCII case-insensitive.
Each metadata key may appear once. An empty sequence is invalid.

State lines use the SDK's complete `set`, `off`, or `fade` command, the three-key
`led=1 color=red fade=20ms` form, or explicit compact data such as
`#FF0000L1T20`. A Fade is a transition, not a hold. Immediate states have zero
fade; positive fades must be exact multiples of 10ms, up to 655350ms. LED values
are 0 (all), 1, or 2. Text lines are valid UTF-8 and at most 4096 bytes, with no
embedded line breaks. State tail comments follow the SDK's whitespace-delimited
`//` convention. Metadata lines do not use tail comments.

Colors in state lines are complete CSS names, `#abc`, `#RRGGBB`, `rgb(r,g,b)`,
or `hsv(h,s,v)` / `hsb(h,s,b)`. HSV/HSB use degrees and percentages. The standalone
`fade` and `blink` CLI commands additionally resolve one exact configured color
alias or an ASCII case-insensitive `random` / `rand` keyword. An alias value is
parsed as one SDK color expression, without recursive alias or random lookup.
`on` and `off` are not color aliases: use `white` and `black`; `off` remains a
state command. For example:

```sh
nb1 act 'fade to HSV(210,75,90) over 20ms on led 1'
nb1 fade 'rgb(1, 2, 3)' --fade-time 20ms
nb1 blink red '#abc'
```

Unknown words, old prose such as `red blink 3 times`, duplicate metadata, and
any malformed state reject the entire file with a one-based line number.
`ParsePlayText` returns no partial PatternSet. SDK errors remain wrapped with
`%w`, so callers can use `errors.As` with `*blink1.ParseError` and inspect any
underlying color error. Commands parse the complete input and preflight decoded
states with `LightState.Validate()` before opening the device. This includes
`act`, `fade`, all colors supplied to `blink`, and files passed to `execute` or
`write`. These checks do not access USB.

`EncodePlayText` returns `([]string, error)`. It validates the complete PatternSet
before emitting canonical `title:` / `repeat:` metadata and compact SDK states.
Export then import preserves RGB, LED, fade time, title, and repeat count.
Invalid data produces no lines, and `convert json2text` checks this result before
creating or truncating its output file. The JSON data codec can represent some
values that hardware cannot play exactly; execution and Play Text export apply
the stricter executable state checks.

This format intentionally replaces the earlier loosely scanned Play Text
sentences. The established Pattern JSON compact codec remains
available. Hardware storage capacity, supported on-device sequence shape, and
explicit Flash writes are separate constraints; a valid file does not promise
that every device generation can store it. `execute` is host playback, while
`write` selects on-device operations. The existing explicit `execute --times 0`
flag continues to mean forever.
