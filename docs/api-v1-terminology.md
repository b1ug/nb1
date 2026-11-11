# API v1 terminology

Use these names in API schemas, CLI help, documentation, examples and Web text.

| Name | Meaning |
| --- | --- |
| Action | A named lighting intent compiled by the core. |
| Sequence | An ordered set of Steps with serial or interval timing. |
| Step | Ordered LED targets, a fade duration and a timing rule. |
| Job | One accepted execution with an ID, progress and terminal status. |
| Pattern | A locally saved, reusable Sequence. |
| DeviceSequence | A sequence represented in a device's native memory format. |

Action kinds are `set`, `off`, `fade`, `blink`, `breathe`, `pulse`, `alternate`, `orbit` and `rainbow`.

Generated Job IDs contain `j_` followed by ten random Crockford Base32 characters. An omitted or empty ID requests generation. The deduplication record covers the entire active Job. Terminal records use a configurable soft TTL from completion and a configurable LRU capacity, defaulting to thirty minutes and 16,384 entries. Profile `jobs.terminal_ttl` / `jobs.terminal_limit` and server `--job-cache-ttl` / `--job-cache-size` select these values at daemon startup. LRU pressure or restart can remove records earlier. The TTL does not guarantee a deduplication window. Queries, matching repeated submissions and terminal cancellation update LRU order without extending the original expiration time. After expiration or eviction, the same ID can be accepted again. These records belong to the running daemon process.
