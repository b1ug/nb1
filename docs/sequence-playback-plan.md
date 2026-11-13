# Sequence playback plans

`internal/sequence.Plan` produces a finite, backend-neutral plan from a validated
Sequence. Its contract is `nb1-sequence-playback/v1`. This is an internal
execution descriptor, not a new authoring format or HTTP endpoint. Actions still
compile to Sequence through the existing Go Action compiler. Color resolution,
Action recipes, repetition limits and timing constants stay in Go.

A browser can compile the same packages to WebAssembly, then interpret these
descriptors using its clock and WebHID transport. It does not need a second
implementation of Action math or timing recipes. This addition does not replace
or change the native daemon executor.

| Field | Meaning |
| --- | --- |
| `initial` | Ordered zero-time writes, applied once before all Steps. Wait `wait_after_ms` after each write returns, including the last. |
| `steps` | Repetition expanded into at most 1024 executed Steps. `index`, `repeat_index`, and `step_index` are zero-based. |
| `targets` | Ordered physical writes with canonical RGB colors and Go-computed fades. `wait_before_ms` starts from the previous target's write return. |
| `wait_after_ms` | For serial Steps, wait after the target returns. Includes its fade, hold and device update allowance. |
| `deadline_ms` | For interval Steps, budget measured from this Step's actual start. Every target write must start and return strictly before this deadline. Wait until the deadline after writing. |
| `settle_after_ms` | On the final interval Step only, also wait until the final target's actual write return plus this duration. This settling wait can overlap the final interval. |
| `finish` | Normal completion only. `off` emits one all-LED black write and an update wait; `hold` emits no additional write. |
| `nominal_offset_ms` | Preview position excluding device I/O and timer lateness. Never an execution deadline. |
| `nominal_duration_ms` | Existing Sequence preview duration. Excludes `finish`, I/O, preflight and timer lateness. |
| `total_target_writes` | Initial, Step and normal finish writes together; at most 2051. Excludes owner preflight operations. |

Serial waits begin after actual write completion. Interval scheduling starts a
fresh budget at every actual Step start, including repetition boundaries. Initial
targets are not repeated. The final settling deadline is based on the actual
last write return; it must not be replaced with a nominal tail or a settling wait
after every Step.

An executor must never catch up a delayed browser or host clock by sending old
Steps in a burst. An interval write that cannot complete within its current
budget fails the run. A browser may additionally fail stopped when a timer wake
is excessively late. Cancellation must interrupt waits and prevent future
writes; it cannot retract a USB write already in flight. The browser's Stop
operation can therefore leave an already started fade running on the device.

Device ownership, autonomous-playback preflight, cancellation, errors and
readback belong to the backend. In particular, an unknown USB write outcome must
not trigger an automatic retry, reconnect replay or extra cleanup write.
`finish` applies only after normal completion. A browser must not run native
daemon cleanup rules implicitly on cancellation or errors.

The planner has no timers, USB access or optical measurements. Passing its Go
tests or building it for WebAssembly establishes software behavior only. Actual
I/O, cleanup success, browser scheduling and observed light still need separate
validation on the relevant hardware and operating system.
