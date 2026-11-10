# Host patterns: authoring and fixed-reference execution

Identify the format from actual schemas. Do not use latest or disable host storage because the device is MK2. A returned library.path is display metadata, not a path for the client to open.

| Format | Storage interface | Execution |
| --- | --- | --- |
| Legacy v1: Program / old Effect | CLI control patterns; MCP list_patterns/get_pattern | preview_program/run_program with {id,revision} pattern_ref |
| Advanced v2: source, normalized action, recipe, frozen execution | CLI control patterns-v2; HTTP /v2/patterns | preview_pattern/run_pattern/get_pattern_request/cancel_pattern_request or matching CLI commands |

The installed 96a baseline has seven advanced CLI commands: get, preview-draft, save, preview, run, request, cancel. Its 16-tool MCP supports advanced fixed-reference execution and tracking. A later CLI candidate adds list; four MCP authoring tools are being qualified separately. Do not assume candidates are installed. Help, tools/list, and schemas are authoritative.

## Save high-level intent without playback

An author file contains id/title/source:

```json
{
  "id": "red-blue-orbit",
  "title": "Red and blue orbit",
  "source": {
    "kind": "light_action",
    "contract": "nb1-light-action/experimental-v2",
    "action": {
      "kind": "orbit", "colors": ["red", "blue"],
      "period_ms": 3000, "cycles": 1, "segments": 4, "on_finish": "off"
    }
  }
}
```

1. Preserve the author file. Run control patterns-v2 preview-draft FILE to obtain the candidate record/revision. This neither saves, prepares a run ticket, nor accesses USB.
2. To save, add expected_revision from that exact preview to the same author JSON, in a separate submission file. This is a candidate-content condition, not overwrite permission or a daemon ticket.
3. control patterns-v2 save FILE submits once. Different content under the same ID conflicts; new content returns201, an identical existing record200. Success is publication_confirmed, not a Job or accepted execution.
4. On publication_unknown or a lost/contradictory save response, retain the original author file, Ref, and revision. Read get ID to compare; do not automatically save again, change ID, or play. Confirmation requires an integrity-verified complete record matching the original contract, ID, and full revision. A match establishes presence now, not which attempt created it. A 404, mismatch, unavailable library, or corrupt result does not prove the original save never happened. An integrity-valid unknown recipe can confirm exact record presence while remaining semantically unsupported and read-only.

CLI reuses local source compilation and full verification. MCP relies on the daemon's authoritative compiler and independently checks record revision, typed execution digest, and Ref; it is not a second source compiler. If a discovered tool returns record_json as a string, retain the exact JSON rather than round-tripping through a generic Number object.

Current P1 requires ASCII object keys at every depth; Unicode string values are supported. Times/counts use ordinary nonnegative JSON integers, not decimals, exponents, or negative zero. Preserve int64 precision. Unknown source/recipe records remain read-only and cannot be newly imported or executed; unknown entries may make the entire library read-only.

The advanced HTTP API also supports list/get, author preview/save, and frozen snapshot import/export. Do not invent missing CLI commands or send v2 data to a v1 import route. Storage operations do not open a device, prepare execution tickets, or rewrite the original source.

Local source remains editable. Publishing a changed period/source under the same existing ID conflicts: preserve the old record and use a user-appropriate new ID for a changed publication. There is no update-in-place command. When advanced MCP authoring tools are absent, use the discovered CLI preview-draft/save/get route rather than inventing MCP names.

## Execute and cancel an exact reference

Obtain the complete reference from a trusted saved record:

```text
pattern_ref = {record_contract: "nb1-pattern-record/experimental-v2", id, revision}
```

Revision binds the whole record. execution_digest binds only frozen execution; they are not interchangeable. Do not extract the raw program and run it through a legacy route while claiming to preserve high-level provenance.

When the advanced tools are discovered:

- preview_pattern({pattern_ref,serial?}) verifies stored source and checks cached capabilities; no Job/ticket.
- Preserve Ref, serial, policy, and the ticket obtained once through list_devices({prepare_request:true}). run_pattern requires an explicit replace/queue/reject policy, usually reject. It sends one POST with no implicit preview/Get/Prepare.
- get_pattern_request({pattern_ref,serial,...originalTicket}) queries the original receipt independently of library health. A trusted job_id/execution_digest can tighten the binding.
- cancel_pattern_request uses the same original identity: GET to verify, then at most one confirmed_job_id conditional POST. Terminal/cancelling jobs cause no further POST. Bad responses retain original intent and the last trusted snapshot.

Matching CLI preview/run/request/cancel commands take ID and the full --revision. Run defaults to reject and accepts explicit policies. A missing ticket causes one Prepare; an original ticket pair causes none. Wait only reads that original receipt. Corruption,404, expiry, or restart never implies rebuilding the task.

A PatternJobEnvelope contains pattern_ref/execution_digest/job_kind/job. A bare FrameJob loses provenance. Full counters do not prove the tail fade/cleanup is complete. Cancelling a completed hold job does not extinguish it.

## Legacy v1

list_patterns returns summaries; get_pattern returns a full record. preview_program/run_program accept pattern_ref exclusively with program/effect. Execution uses explicit reject and the original ticket, with no dry_run field, including false. A v1 Action's frozen result.program can be stored, but source.action and FrameProgram cannot.

CLI control patterns import does not play. Export exclusively creates its output file without overwriting or creating parent directories. Keep publication_unknown distinct from execution outcome_unknown. Host storage and device flash have separate capabilities.
