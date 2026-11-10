# API reference examples

These 14 JSON files are input data. Opening or saving a file does not play anything. The [English API reference](../../api-reference.md) explains their contracts and lists each example; the [Chinese example guide](README.zh-CN.md) is retained.

The eight v1 `.action.json` files contain `contract/action` for `control light preview/run`: set, off, fade, blink, breathe, pulse, alternate, and rainbow. `orbit.action.json` uses the experimental-v2 light contract and compiles to paired FrameProgram fades. Preview does not create a Job or access USB.

`low.program.json` is an exact sequential Program; `legacy.effect.json` uses legacy Effect semantics; `low.frame-program.json` is raw experimental FrameProgram input for HTTP. `hsv.color-input.json` is a bare ColorInput value, so wrap it as `{"color":...}` for the resolve HTTP endpoint.

`pattern-v2.preview.json` is an advanced host Pattern author request for `/v2/patterns/preview`, supported by installed96a and absent from historical1d89. Its title is preserved user data. Saving requires confirming the complete preview revision and adding expected_revision; it is create-only and does not play.

Execution additionally requires an actual selected serial, the appropriate explicit policy, and the original prepared ticket. None of these files contains a fake device binding or expired ticket. The example bytes and any content-bound data remain unchanged during English documentation updates.
