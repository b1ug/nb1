# nb1 v0.1.4 — Keep the light in rhythm

When several things happen at once, your blink(1) can now keep its place. This release adds per-device queues and explicit preemption, a guarded way to choose a startup look, and a clearer front door to the local daemon.

## Queue a moment, or let the urgent one lead

A new Job can wait in that device's queue or preempt its current Job. Each blink(1) has an independent scheduler, so work on one light does not interrupt another. Preemption cleans up the current Job before starting the urgent one, then keeps the waiting Jobs in order. Existing requests still reject a busy light unless they opt in to a policy.

## Choose a startup look

Use `nb1 device reset SERIAL [off|rainbow|starlight|fireflies]` or the embedded Workbench to stage a built-in startup pattern on a supported MK2. The command validates each persistent row before making one Flash-save request. It refuses busy devices, binds the operation to the selected serial and does not replay an uncertain save automatically. MK3 startup configuration uses a different path and is not covered by this command.

**Flash acknowledgement note:** On macOS, the USB stack can fail to confirm a Flash save even when the device has stored it. In physical MK2/FW204 acceptance, one Rainbow request returned HTTP 502 with an `unconfirmed` result; two independent cold reads matched all 32 rows afterward. The original pattern was restored and also matched two cold reads. nb1 keeps the result marked unconfirmed because the low-level acknowledgement was missing. If you see that result, inspect the device before trying another save.

## A clearer front door

`GET /` now returns a read-only JSON welcome and endpoint directory with build identity, the configured device policy and cached device counts. It does not open USB. `nb1 version --json` and `nb1 doctor --json` also report release identity correctly when built from a source archive.

## Take a look

Open the embedded Workbench at <http://127.0.0.1:5885/demo/>. Queue and preemption behavior is available through HTTP, CLI and MCP. See the [API guide](https://github.com/b1ug/nb1/blob/v0.1.4/docs/api-v1.md), [device reset guide](https://github.com/b1ug/nb1/blob/v0.1.4/docs/device-native-sequence.md), and [release downloads](https://github.com/b1ug/nb1/releases/tag/v0.1.4).

Physical acceptance covers two MK2/FW204 units. It does not claim MK3, every firmware revision, every operating system's USB acknowledgement behavior, or optical color calibration.
