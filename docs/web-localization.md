# Embedded bilingual Web candidate

This development candidate imports the separate `nb1-web` producer commit `1e1b1ddf2a985dea00e494761eed592183755c1d`, with manifest SHA-256 `a35beb8fb17aa3a9e6751e006e0c7b6524d197b83cd679a25528a99134ce6460`. Its generated payload is 260,151 bytes across 13 files, including the manifest and license. The 256 KiB bundle limit is unchanged; 1,993 bytes remain.

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

The existing generated API reference, OpenAPI and route inventory describe the captured 44-operation baseline. This source has 46 route/method entries after adding these two static resources; the generated snapshots have not yet been refreshed. The JSON models and examples are unchanged. This distinction is intentional until the documentation generator captures the complete new candidate.

## Reproduce the import

Use the official importer against a reviewed local producer directory:

```sh
python3 tools/import-web-dist.py /path/to/nb1-web/dist \
  --source-commit 1e1b1ddf2a985dea00e494761eed592183755c1d \
  --manifest-sha256 a35beb8fb17aa3a9e6751e006e0c7b6524d197b83cd679a25528a99134ce6460 \
  --check
```

Use `--dry-run` to also validate the existing destination. Omitting the mode flag imports the verified bytes and updates the source-bound lock. Normal import failures restore the previous pair; crashes and concurrent unrelated edits are not promised atomic recovery. No frontend download or build runs in the importer, and generated `dist/` files must not be edited manually.

This is an uninstalled local source candidate. The installed workspace suite remains source `1e2e7650e2cff6686d95cb9908953e5fd370f2bc` with the earlier English bundle. No daemon, USB device, camera, service configuration, remote branch or release is changed by preparing this candidate.
