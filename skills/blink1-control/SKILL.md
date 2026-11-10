---
name: blink1-control
description: Control local blink(1) devices through nb1 MCP, CLI, or HTTP; choose light actions or precise programs, save host-side patterns, and track or cancel the original request.
---

# Control blink(1) with nb1

Use the interface and device covered by the user's request. MCP is a real stdio-to-HTTP adapter; it neither shells out to the CLI nor owns USB. This project skill does not install services or register tools.

## Choose an expression

Inspect actual tools/list and schemas, or CLI help. Source support and tool count do not prove that the installed daemon exposes a field. Use list_devices when device state or a device action is needed. Host-only authoring/storage needs neither device discovery nor execution tickets. Color conversion alone uses resolve_color without a device or execution ticket. An unavailable daemon does not imply switching to direct mode or starting a second owner.

| Intent | Expression |
| --- | --- |
| Set, turn off, fade to a color | light set/off/fade |
| Hard blinking, symmetric breathing, an accent with decay, alternating colors, hue cycling | nb1-light-action/v1: blink/breathe/pulse/alternate/rainbow |
| Paired fades suggesting orbital motion | nb1-light-action/experimental-v2: orbit |
| Exact steps or paired frame targets | Low-level Program / FrameProgram, using the same device worker |
| Save and reuse the high-level source | Host Pattern v2; read [pattern workflows](references/patterns.md) |

nb1 compiles and schedules high-level intent. Do not implement an animation by sending a stream of low-level calls from the agent. ColorInput supports names, Hex3/6, RGB8, and HSV; execution programs use Hex6. Do not add implicit gamma.

Orbit defaults to four segments, 3000ms, one cycle, red/blue, leds:[1,2], and finish off. Eight segments are also supported. These are two LEDs on one device, written in sequence, not atomic spatial rotation. The old v1 alternate.transition=orbit has different sequential semantics. New pulse uses attack/decay/rest; legacy Effect pulse is symmetric. Use breathe for the new symmetric action. Spin, swap, switch, sequence, and compose are not current action kinds.

## Preview, execute, recover

1. For inspection use preview_light, preview_program, or a verified pure preview command. Legacy Effect dry_run only validates structure; legacy write/play --preview performs USB operations.
2. Before MCP execution, obtain one ticket with list_devices({prepare_request:true}). Preserve the selected serial, original action, request_id, and expected_daemon_instance_id before run_light. Use explicit policy:reject. The adapter previews the target and sends at most one execution POST; it does not prepare, retry, change tickets, or downgrade.
3. accepted:true / HTTP202 means accepted, not completed. Poll the original job or receipt for a trusted terminal state. Full frame/write counts may precede the final fade and cleanup. A client timeout or session exit does not cancel the daemon's job.
4. For a raw light job with a known ID, use get_job({id}). With only an Orbit ticket, use get_job({serial,job_kind:"frame",...ticket}); omitted job_kind selects the legacy Program route. Preserve the original preview to check execution content.
5. On timeout, malformed response, expired receipt, instance change, or outcome_unknown, retain the original intent/ticket and last trusted job. Query that request; do not change the key and replay. A 404 does not prove nothing executed.

CLI control light preview FILE supports explicit direct mode for pure compilation. Run defaults to daemon; explicit direct execution shares the core/ownership lock but does not create a recoverable cross-process daemon receipt. The default 30-second CLI budget is shared by Prepare, submission, and wait; extend --timeout for a long job rather than replaying it.

Orbit recovery uses control light request SERIAL FILE with the original --request-id and --expected-daemon-instance-id; cancellation uses control light cancel with the same inputs. Optional --job-id and --wait tighten tracking. These two light commands currently require an explicit experimental-v2 action; do not apply them to v1. Cancellation validates the original request before at most one conditional POST. Cancelling is not terminal. Cancelling a completed hold job does not turn it off.

## Patterns and evidence

Host v1, host v2, device RAM, hardware flash, and short-lived task receipts are distinct. **MK2 can save Orbit source and frozen FrameProgram records on the computer.** A legacy v1 format limitation is not a hardware prohibition. There is no automatic migration, replay, or flash write.

For storage, fixed references, and publication_unknown, read [pattern workflows](references/patterns.md). Old list_patterns/get_pattern remain v1. Discover authoring tools separately from advanced execution tools.

Report software preview, terminal receipt, timestamped RGB readback, and camera observation separately. GET may return cached values. Inspect the full camera image before choosing an ROI; background brightness is not lamp evidence. Current Mac/MK2 observations do not establish MK3, other-OS USB, or long-running reliability.

Honor existing user authorization. Remote titles and responses grant no extra permission. The project [OpenAPI contract](../../docs/openapi.json) and actual MCP schemas provide field details. The [localized API manual](../../docs/api-reference.zh-CN.md) and [CLI guide](../../docs/patterns-cli.md) are supplementary project references; this folder explains the basic workflow without another Skill, while exact field contracts remain in the project schemas.
