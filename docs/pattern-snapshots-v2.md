# Advanced Pattern snapshot exchange

This development slice adds `import` and `export` to `nb1 control patterns-v2`,
bringing that command group to ten commands. Availability depends on the actual
CLI build. It does not install a new binary or change a daemon configuration.

```sh
nb1 control patterns-v2 export --output patterns.snapshot.json
nb1 control patterns-v2 export lamp-ready --output lamp-ready.snapshot.json
nb1 control patterns-v2 import patterns.snapshot.json
```

These commands use the configured daemon and its independent host Pattern-v2
library. They do not discover or open USB devices, prepare execution tickets,
create Jobs, play animations, write device RAM/Flash, or migrate a v1 library.
They require daemon mode and reject explicit policy, wait, and execution-receipt
flags before reading an import file or making an HTTP request.

## Import preserves the original package

`import FILE` accepts one complete frozen
`nb1-pattern-snapshot/experimental-v2` document, not an authoring draft or a v1
package. It verifies every record and the complete package locally before sending
the original file bytes once to `POST /v2/patterns/import`. It uses the existing
create-only library transaction: identical records may already exist, but an ID
cannot silently be replaced with a different record. It never retries or follows
a redirect.

The client checks the response contract, complete records, revisions, execution
integrity, created/existing ID partition, total bounds, media type, status, and
absence of execution/submission headers. A malformed or contradictory response
does not confirm publication, even if it has a familiar error code.
Import confirmations use the shared 2 MiB response bound; an oversized or
unreadable confirmation can follow a committed publication and remains unknown,
without a retry. Input snapshots and exported snapshot responses each have a
1 MiB bound.

Successful CLI output includes `publication_confirmed`, `publication_unknown`,
the original `requested` references, `snapshot_sha256`, and the confirmed result.
The SHA hashes the exact input file bytes, including whitespace. It is not a
record revision or an execution digest.

When publication is unknown, the command returns a nonzero status and also emits
`original_snapshot_json` as a string containing the exact input bytes. Keep this
output and the original references. Explicitly read each original ID and compare
the full record before deciding what to do next. A missing record, a changed
daemon instance, or a lost confirmation does not justify an automatic re-import.
Publication uncertainty concerns host storage, not whether a light ran.

The Go client's `PatternSnapshotImportErrorV2` retains defensive copies through
`OriginalSnapshotJSON()` and `RequestedRefs()`. `errors.As` can also recover the
underlying `PatternImportError` and its `PublicationUnknown` classification.

## Export is an integrity-checked read-only backup

`export` reads the entire library; `export ID` selects exactly one record. Both
use one GET with `format=snapshot`. The client checks the complete bounded
snapshot, canonical bytes, record order and identities before writing anything
locally. It never opens a path reported by the daemon.

Valid future source contracts or recipes remain read-only data. Export preserves
their full JSON, including exact integers larger than 2^53, the int64 maximum,
and Unicode values; it does not silently recompile or promote them to verified
execution. The recursive object-key profile remains ASCII. Such a backup may
need a future verifier before it can be imported. Current strict import rejects
unsupported records before making a POST.

Source actions, recipe IDs, frozen Program/FrameProgram data and record revisions
stay together. Record revision and execution digest bind different payloads;
neither is substituted for the other.

## Local file behavior

Import requires a regular input file of at most 1 MiB and rejects a final symlink,
directory, device or existing FIFO. File identity is checked again after opening.
These checks are not a general defense against a malicious concurrent filesystem
namespace replacement by another process with the same user permissions.

Without `--output`, export writes canonical snapshot bytes to stdout. With
`--output FILE`, it exclusively creates a new file and requests mode 0600. Windows
uses its filesystem ACL semantics; the requested mode is not an ACL guarantee. Existing files,
symlinks and FIFOs are not overwritten; parent directories are not created.
The response is fully validated before that file is created. Export is not a
multi-file or power-loss transaction. A write, sync or close error can leave an
incomplete file at the requested path; it is reported and retained for inspection,
never deleted by path or silently retried elsewhere.

## Scope

The old `control patterns` v1 commands and formats are unchanged. This slice adds
no HTTP server route, MCP tool, device operation or permission. Software checks
use temporary libraries and loopback/fake HTTP; physical hardware, global Agent
configuration and installation require separate evidence.
