# Web components and build boundaries

[简体中文](web-components.zh-CN.md)

nb1 has three different Web surfaces. Choose the one that matches the device
owner you want; they are separate deliverables.

| Name | Source | Purpose and device owner |
| --- | --- | --- |
| nb1 Guide | [nb1-guide](https://github.com/b1ug/nb1-guide) | Static English/Chinese guides. It does not request USB permission or control a lamp. |
| nb1 Debugger | [nb1-debugger](https://github.com/b1ug/nb1-debugger) | Built into nb1 and served at `/demo/`. It calls the same-origin HTTP API; the nb1 daemon owns USB. |
| nb1 WebHID | [nb1-webhid](https://github.com/b1ug/nb1-webhid) | Independent WebHID workbench development preview and experimental Chrome reminder extension. The page or extension owns USB directly. |

The embedded workbench is part of the same `nb1 server` process as its HTTP API;
it is not another daemon to install. Direct WebHID tools require an explicit
ownership handoff from native nb1 or another browser controller. Browser access
does not participate in native nb1 device locks.

The experimental extension is not a Chrome Web Store release. Its physical
background-reminder acceptance remains separate from browser software checks.
Internal planning and research pages are not public product entry points.

## Go modules, source checkouts and embedded assets

Go modules such as GID, blink1-go and colorkit supply Go code through `go.mod`.
The frontend is a separate source project rather than a Go module dependency.

The debugger consumes the MK2 assembly, original ThingM meshes and license from [blink1-models](https://github.com/b1ug/blink1-models) at a fixed Git commit. Its npm lockfile and model manifest bind the input bytes. The standalone Go build uses the imported files offline.

The frontend build records its source repository, exact commit, clean-tree
state and per-file hashes. nb1 imports only that reviewed `dist` inventory and
records the chosen commit and manifest hash in
`internal/webui/manifest.lock.json`. `go:embed` puts the verified assets in the
executable. The public bundle excludes internal capture records, research notes
and workstation paths; original artwork, model geometry and licenses remain.

An ordinary nb1 Go build uses those committed assets. It does not run Node,
download a frontend repository or require `git clone --recursive`. GitHub source
archives and Homebrew source builds therefore contain the assets they need.

A Git submodule can pin a developer's source checkout, but it does not build or
validate `dist`, collect licenses or replace the embedded manifest. This project
keeps the explicit bundle import as its release boundary.

## Updating the embedded workbench

Build and review a clean workbench checkout using that repository's instructions.
Review its full commit and `dist/manifest.json` SHA-256, then from nb1 run:

```sh
python3 tools/import-web-dist.py ../nb1-debugger/dist \
  --source-commit WORKBENCH_COMMIT \
  --manifest-sha256 MANIFEST_SHA256 --check
```

Replace both placeholders with the reviewed full values. `--check` validates
the source without writing anything. Remove `--check` to import the bundle,
review the resulting asset and lock changes, and run:

```sh
python3 tools/test_import_web_dist.py
GOWORK=off go test -mod=readonly ./internal/webui ./internal/httpapi
```

Frontend source and nb1 host changes remain separate commits/PRs, linked by the
exact bundle pin. Documentation links and browser releases have their own
publication checks. Publishing one surface does not establish that another was
rebuilt, installed or physically tested.
