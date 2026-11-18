# Nightlab embedding contract

The workbench at `/demo/` defaults to Nightlab and English, with explicit language and appearance choices. The complete local 3D model, two-face fallback, screen playback, model notes, original CAD and licenses are embedded in the executable. No Node server, CDN, startup download, Service Worker or extra installation is needed.

The production controller retains the current API v1, Pattern/color library, JSON editor and Job recovery behavior. Screen playback, editing, language, appearance and camera changes create no Job and write no hardware. Draft targets, screen frames, accepted Job targets and cached observations remain distinct.

Play resolves and checks the current draft on click, then submits the returned canonical Sequence as one captured Job; a separate Screen preview is optional. Solid can apply one color or independent LED 1/LED 2 colors. The pair uses two zero-fade serial Steps in one Job with finish hold, preserving both colors without introducing a new Action or API.

The compact mobile view orders output choices as both/same, separate colors, LED 1 and LED 2. Saving, help and request details use disclosures, while actionable playback status, Stop and uncertain-result recovery remain visible. Confirmed completion does not mean Off, and a failed status refresh does not replace an established terminal result.

The color-set chooser keeps nine common utility swatches separate from eight curated themes. Every theme has exactly eight colors in a consistent hue order, for 64 theme positions; shared colors use exactly the same values across themes. Each theme has an explicit pair independent of the displayed order. Browsing changes the available swatches; choosing a swatch edits one color, Use pair fills both slots, and Create Sequence drafts the selected theme's eight serial color transitions. The utility bank does not generate a nine-color Sequence. A read-only comparison table groups the same catalog by theme and hue. These controls do not start preview or playback, submit a Job or save a Pattern automatically. Editable inputs and the normal Preview/Play actions remain authoritative.

## Closed profiles and provenance

The legacy schema-1 `static-editor/v1` profile retains 26 assets plus its manifest and a 640 KiB total limit. The schema-2 `static-nightlab/v1` profile has 57 assets plus its manifest. Its limits, including the manifest, are:

| Budget | Limit |
| --- | ---: |
| Complete stored bundle | 6 MiB |
| Complete decoded bundle | 16 MiB |
| Decoded base UI | 768 KiB |
| Manifest JSON | 32 KiB |

The base budget includes the shared presentation code. The complete model/compiler/help resources are excluded from that base subtotal and remain included in both complete-bundle limits.

Manifest top-level keys remain `schema_version`, `product`, `asset_base`, `source`, `build`, `api_contracts` and `files`. Every schema-2 file records `path`, `bytes`, `sha256`, `encoding`, `decoded_bytes` and `decoded_sha256`. The path is both its logical and stored filename, including compressed `.js`, `.wasm` and `.stl` files. Stored length/hash and decoded length/hash are independently checked; `identity` requires both pairs to match. Gzip accepts exactly one complete member with no trailing data, bounded before and during decoding.

The canonical producer is [nb1 Embedded Workbench](https://github.com/b1ug/nb1-workbench). Its manifest records that public repository URL and a clean source commit. Development capture records, research notes and local workstation paths are excluded from the public bundle. The importer checks decoded assets for workstation-specific paths, and production-bundle tests enforce the same publication boundary. Original geometry and approved artwork are retained.

The source tree digest continues to cover sorted stored-file hashes. `manifest.lock.json` retains its schema-1 lock shape and binds the clean producer source commit and exact manifest SHA-256. The frontend builder, Python importer, Go validator and HTTP MIME/routes each retain closed reviewed inventories. Unknown files, directories, traversal, links, duplicate declarations, unsupported encodings and exceeded limits fail closed. Read the actual producer pin and current file hashes from the generated lock and manifest.

Large resources are stored compressed in the binary. The HTTP layer returns their verified decoded bytes with the original no-store/nosniff behavior; it does not add HTTP content-encoding negotiation. Original CAD and approved nb1 artwork bytes remain unchanged after decoding.

## Routes, CSP and planning

Exact resources live under `/demo/` and `/demo/nightlab/`; there is no general directory server. Only the workbench HTML entries accept one optional `skin=nightlab|airwave|instrument` and one optional `view=faces`. Unknown, duplicate, empty or malformed queries are rejected. Static resources and API routes retain their no-query rules.

Nightlab HTML adds only `'wasm-unsafe-eval'` to the existing same-origin script policy. JavaScript `'unsafe-eval'`, `'unsafe-inline'`, remote origins and broad data/blob permissions remain absent. Each HTML source contains exactly one canonical `nb1-style-nonce` placeholder. The host replaces it with a fresh per-response nonce; CodeMirror uses that nonce for generated styles. Other CSP directives remain unchanged.

The daemon's `/v1/preview` remains the authoritative compiler and alias resolver. Screen playback sends its returned canonical Sequence to the paired local Go/WASM `planSequence`, then samples the resulting `nb1-sequence-playback/v1` descriptor. It does not compile current colors or Actions through the older browser compiler, reproduce recipes in JavaScript, or introduce a planner HTTP API. The bundled planner pin has source-equivalent Sequence/planning packages to the selected host base; future planner changes require a newly reviewed paired compiler/runtime and receipt.

The model keeps LED 1 on the blink(1) face and LED 2 on the ThingM face, the corrected USB mating geometry and continuous additive light field. Loading, missing WebGL and context loss retain the two-face view; `?view=faces` selects it directly. These illustrations are not optical measurements or manufacturing CAD.

## Software verification

Run `GOWORK=off go test ./internal/webui ./internal/httpapi` and `python3 tools/test_import_web_dist.py` after changing host boundaries. Import only a clean reproducible frontend build with explicit source and manifest pins. Browser acceptance uses the existing disposable software fixture with `NB1_V1_WEB_FIXTURE_DIST=embedded`; it never selects a native driver. Check the real host's CSP/nonce, all local assets, nine effects, screen controls, cancellation, languages, narrow layouts, fitted joints and fallbacks before a separately authorized runtime replacement.
