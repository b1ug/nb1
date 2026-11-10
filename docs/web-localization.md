# Embedded bilingual Web in installed6ca

Installed runtime6ca imports the separate `nb1-web` producer commit `1e1b1ddf2a985dea00e494761eed592183755c1d`, with manifest SHA-256 `a35beb8fb17aa3a9e6751e006e0c7b6524d197b83cd679a25528a99134ce6460`. The generated payload is 260,151 bytes across 13 files, including the manifest and license. The 256 KiB bundle limit is unchanged; 1,993 bytes remain.

The three tools at `/demo/`, `/demo/orbit.html`, and `/demo/patterns.html` default to English on first use and offer an explicit English / 中文 selector. A namespaced browser preference carries the user's selection across pages. Browser and OS language do not change the first-use default. Translation catalogs can be extended in the producer; nb1 consumes its fixed runtime assets without requiring Node at build or startup.

Localization changes presentation. Action kinds, color values, IDs, revisions, source JSON, frozen records, recipes, original tickets and daemon payloads retain their machine or user-authored values. Selecting a language does not submit a job, rewrite a record or request USB access. Producer browser qualification is separate from this consumer's bundle and HTTP verification; neither establishes optical effects or device compatibility.

## Closed profiles and routes

The importer and Go validator accept exactly three complete profiles:

| Profile | Declared assets | Files including manifest | API contract declarations | Public static GET resources |
| --- | --- | --- | --- | --- |
| Base | 7 | 8 | Two original light contracts | 6 |
| Pattern | 10 | 11 | Two light and two Pattern contracts | 9 |
| Bilingual Pattern | 12 | 13 | The same four contracts | 11 |

The bilingual profile requires both `i18n.js` and `locales.js` in addition to every Pattern asset. Partial profiles, substituted base/Pattern files, extra files, unknown or duplicate declarations, source mismatch, manifest mismatch and content-hash mismatch fail validation. All profiles retain the same aggregate 256 KiB limit including manifest. Both locale assets must be declared, pinned and served locally; the license and manifests remain private to the embedded bundle.

The only added routes are `GET /demo/i18n.js` and `GET /demo/locales.js`, both JavaScript with the existing same-origin, no-query, no-body, GET-only, no-store and CSP rules. A valid older bundle lacks these files and returns 404 for them. No light, Pattern, SDK, MCP, authorization or execution contract changes.

The API reference, OpenAPI and route inventory now describe installed source 6ca: 46 canonical operations, 50 OpenAPI operations including four ordinary-ID aliases, 45 paths and 111 unchanged models. Only these two static GET entries are new;14 JSON input examples and the20 complete MCP tool objects are unchanged. Installed6ca has46 operations/CLI10/bilingual Web; previous1e44/CLI8/English Web is historical. The separate historical44-operation Recorder captures are preserved and are not relabeled as new HTTP/native observations.

## Reproduce the import

Use the official importer against a reviewed local producer directory:

```sh
python3 tools/import-web-dist.py /path/to/nb1-web/dist \
  --source-commit 1e1b1ddf2a985dea00e494761eed592183755c1d \
  --manifest-sha256 a35beb8fb17aa3a9e6751e006e0c7b6524d197b83cd679a25528a99134ce6460 \
  --check
```

Use `--dry-run` to also validate the existing destination. Omitting the mode flag imports the verified bytes and updates the source-bound lock. Normal import failures restore the previous pair; crashes and concurrent unrelated edits are not promised atomic recovery. No frontend download or build runs in the importer, and generated `dist/` files must not be edited manually.

Fixed runtime `6ca5a14e9f7fec8a5766e79f5bdef3fafef66d8f` is installed through a separate controlled four-file promotion. Its 57 read-only postinstallation checks cover exact binaries, CLI10, MCP20 and 11 static resources. Four additional isolated browser checks cover EN first use,explicit ZH/navigation/EN roundtrip, raw JSON/title/original session invariance and no application mutations. This is read-only UI verification, not playback or optical qualification. Previous1e English-only installation evidence remains historical. The documentation/import instructions themselves do not install a daemon, access USB/camera, change OS/global MCP settings, push a remote branch or create a release.
