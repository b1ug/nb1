# Trigger mapping input

`New(libraryDir)` reads the optional `<libraryDir>/triggers.json` file. Construction
creates nothing. Every `Get` or `List` reads a fresh, complete mapping; it does not
reuse a previous successful file after an invalid edit.

```json
{
  "schema_version": 1,
  "triggers": {
    "build.done": {
      "pattern_id": "saved-v1-id",
      "serial": "20000001",
      "title": "Build finished"
    },
    "success": {
      "pattern_id": "saved-v1-id"
    }
  }
}
```

Names are exact and case-sensitive: `^[a-z][a-z0-9.-]{0,63}$`. Titles are optional
display content, not aliases. Pattern IDs use the existing saved-v1 grammar
`^[A-Za-z0-9_-]{1,64}$`; they are not file paths. An optional serial must contain
eight hexadecimal digits, preserved unchanged. An explicitly empty serial is
invalid. The parser does not check whether the Pattern or device currently exists.
The caller resolves the saved Pattern, target precedence and device availability.

The file is limited to 1 MiB and 256 mappings; titles permit 256 Unicode
characters. Strict shared JSON validation rejects duplicate/unknown fields,
explicit null, malformed Unicode and excessive nesting. A bad entry rejects the
whole file. Inline Actions/Sequences, policy and composition are outside this
mapping schema.

`List` sorts by name and returns a non-nil empty slice when the optional file is
absent. `Get` returns `ErrNotFound` for an absent file or name. Invalid input uses
`ErrInvalid`; other filesystem failures use `ErrUnavailable` and retain the
underlying error. Callers can validate request names with `ValidName` separately
from reporting a damaged local mapping file.

Readers reject non-regular files and leaf symlinks. Native-platform opening uses
the same no-follow/nonblocking or reparse-point guards as application config.
Atomic editor replacement can publish a new complete file for subsequent reads;
concurrent in-place writes do not gain transaction semantics. This package has no
device access, Pattern execution, network requests, queue or filesystem writes.
