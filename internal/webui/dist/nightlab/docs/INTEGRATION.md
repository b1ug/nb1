# Integrating the nb1 Nightlab reference

This package is a working design reference for a future integration. Nightlab is the selected appearance. The product remains **nb1**; **blink(1)** is the hardware. The package does not replace the browser transport, daemon, compiler, or embedded-asset release process.

中文提示：先运行本包看最终效果，再把展示层分阶段接入选定分支；不要整目录覆盖现有项目，也不要把演示后端当成正式设备层。

## 1. Inspect the delivered reference

From the package root:

```sh
node verify.mjs
node serve.mjs
```

Open `http://127.0.0.1:3041/`. The server is loopback-only and supplies software devices. The embedded and browser workbenches are separate examples. `?view=faces` exercises the same two-face fallback without creating a WebGL context.

The maintained files are in `source/`; `demo/` is generated. For development:

```sh
cd source
npm ci --ignore-scripts
npm run build
```

The build writes `../demo`. It uses the copied, pinned resources and does not build or import another repository's frontend. Node.js 22.12 or newer is required for source development. Browser checks require a locally available Playwright Chromium installation. Runtime assets remain local even if dependency installation requires a registry connection.

Keep a pristine copy or version-control tag of the delivered handoff before editing. `MANIFEST.json` describes the delivered snapshot; a mismatch after deliberate development is not a reason to conceal the change by replacing its recorded hashes. Record a new reviewed snapshot for a new delivery.

## 2. Select the actual integration branch first

The following are local observations made on **2026-11-15**. Check them again before migration:

| Tree | Observed revision | Meaning |
| --- | --- | --- |
| Original embedded design baseline | `884f77ef00b77d8126a6e777213a202dccc1da6a` in `validation/nb1-web-v1` | The source copied before this design study. |
| Current `nb1-web` | `99afba38ef88825fd3b4ad8e708fce60199118e4`, clean | Differs from the embedded baseline in 23 source files, including deleted assets and changed business code. |
| Current `nb1-browser` | `1ab6203e30343a597bf5166149d2592135bae802`, clean | Matches the browser baseline copied for the study. |
| Pinned browser Go compiler | `72c2f097319bc7ce0086204b8e713c8b3f16cbbc` | The compiler and planner behind the delivered WASM; its receipts are preserved. |

From the package root, inspect the sibling repositories without changing them:

```sh
git -C ../nb1-web status --short
git -C ../nb1-web rev-parse HEAD
git -C ../nb1-browser status --short
git -C ../nb1-browser rev-parse HEAD
```

The workspace contains multiple integration and validation trees. Do not assume that a directory named `nb1` or `nb1-web` is the intended current integration branch. `validation/nb1-release-readiness` contains a useful example of the newer verified embedded-asset pipeline, but it is a reference to inspect, not a destination this handoff has selected for the team.

Read the destination's `AGENTS.md` and API/build contracts. The user's selected Nightlab direction supersedes the study's earlier Animal Crossing appearance; protocol, localization, asset provenance, and device ownership requirements still apply. Resolve branch drift file by file rather than restoring older business JavaScript from this package.

## 3. Move the presentation layer in stages

| Reference files, relative to `source/` | Integration purpose |
| --- | --- |
| `airwave-tokens.css`, `studio-skins.css` | Semantic colors, four type roles, Nightlab surfaces, selection, focus, disabled, error, and execution states. Historical `airwave` filenames remain for stable imports. |
| `embedded/src/*.html`, `embedded/src/airwave.css`, `embedded/src/studio.css` | Embedded layout reference. Reproduce the composition in the selected `nb1-web/src` while preserving required control IDs and current handlers. |
| `browser/src/web/index.html`, `browser/src/web/airwave.css`, `browser/src/web/studio.css` | Browser workbench layout reference for `nb1-browser/src/web`. |
| `studio.css`, `studio.js`, `composer-ui.js`, `studio-appearance.js` | Shared composition, contextual parameter labels, preview surfaces, and appearance controls. |
| `studio-preview.js`, `preview-timeline.js` | Explicit screen playback from the compiled Sequence and the pinned Go `planSequence` descriptor. |
| `studio-3d.js`, `light-model.js`, `enclosure-fit.js`, `model-assets/` | Optional local Three.js viewer, continuous light illustration, and corrected derived enclosure. |
| `vendor/browser-compiler/`, `browser/src/shared/compiler-loader.js` | Paired Go/WASM runtime and loader. Preserve compiler provenance, or rebuild the complete compiler through the destination's reviewed process. |

Recommended sequence:

1. Create an isolated integration branch or worktree in the team's selected target. Keep the delivered reference available for visual comparison.
2. Port tokens and Nightlab styles, then the compact layout and the custom menus. Keep existing transport, validation, Job recovery, and locale machinery.
3. Establish a lightweight two-face view with explicit target states. Validate this base before adding 3D.
4. Connect screen playback to the destination's actual compiled Sequence and matching Go planner. Verify state cancellation and language changes.
5. Add optional 3D only after deciding the asset, route, CSP, and payload policy below.
6. Run software acceptance in the chosen destination. Integration with a daemon or physical unit is a later, separately recorded acceptance step.

The reference CSS order is: original application styles → shared tokens and application `airwave.css` → shared `studio.css` → application `studio.css` → `studio-skins.css`. Inspect the generated HTML for the exact order. The appearance initializer runs early to avoid a flash of the old theme.

Nightlab is the default in this delivery. If adapting appearance selection, preserve explicit `?skin=` overrides. Changing a default also requires updating the menu's URL logic: deleting the query parameter must mean the default appearance. Do not pre-set an HTML appearance attribute in a way that makes the existing initializer ignore an explicit query.

## 4. Keep the demo facilities out of production

The following implement the **software example**, not the production device layer:

- `review-runtime.js`: installs a fake `navigator.hid` and counts simulated feature reports.
- `scripts/serve.mjs` and `scripts/compiler.mjs`: loopback review API, memory-only Jobs, colors, Patterns, and server-side WASM execution.
- `NB1_DESIGN_SIMULATION`, `/__review/state`, software serial `design-light-01`, simulated ownership wording, and the software banner.
- The handoff builder's replacement of the real browser ownership confirmation and its generation of review-only download/landing pages.

Do not copy the generated `demo/` into a production asset tree. Its HTML intentionally activates the simulation. Integrate source changes through the destination's own builder and retain the actual connection and ownership flow.

`review-runtime.js` also demonstrates cached sample-time formatting. Move that small presentation behavior into the appropriate production view if needed; do not import the fake HID adapter to obtain the date formatter. Preserve the original ISO timestamp, show the timezone/UTC offset, and keep milliseconds, durations, local dates, and wall-clock timestamps distinct. An unavailable or invalid timestamp should remain unavailable.

## 5. Adapt paths, assets, and the host contract explicitly

This sample is served at an origin root with `/browser/` and `/embedded/`. A production `/demo/` mount or independently hosted browser tool needs an explicit asset base:

- `studio-preview.js` currently imports `./browser/shared/compiler-loader.js`. A standalone embedded package must supply an appropriate shared compiler location.
- `studio-3d.js` uses relative Three.js/STL module URLs and an absolute `/model.html` information link.
- `studio.js` and `composer-ui.js` currently identify the browser example with `location.pathname.includes('/browser/')`. Replace this convention with an explicit app configuration or an adapter for the chosen mount.
- Preserve local fonts, image resources, MIME types, compiler licenses, and the matching `wasm_exec.js`. Runtime use must not depend on a CDN or startup download.

The existing asset contracts cannot accept this full sample unchanged:

| Locally inspected builder/host | Existing constraint |
| --- | --- |
| Current sibling `nb1-web/scripts/build.mjs` | Exact source-file inventory, CSS resource restrictions, and a 256 KiB payload limit. |
| `validation/nb1-release-readiness/internal/webui/embed.go` and `tools/import-web-dist.py` | Closed asset allowlist, source-bound manifests/locks, and a 640 KiB payload limit. |
| The newer host's `internal/httpapi/light.go` | Explicit `/demo/*` asset routes, not an arbitrary static directory. |

The official model assets alone are about 5.3 MB; local Three.js and Go/WASM increase the payload. Decide whether the base embedded UI stays lightweight and 3D becomes an optional local asset package. Any changed limit or allowed asset type must be deliberate and covered by import, routing, manifest, and serving tests. Do not bypass integrity checks or globally relax their budgets just to copy the example.

The newer host's CSP uses `script-src 'self'`; the review server explicitly supports WASM with `'wasm-unsafe-eval'`. A production WASM integration needs its own policy decision and verification. CodeMirror uses a per-response style nonce through exactly one canonical `meta[name="nb1-style-nonce"]` placeholder. Retain the host's nonce substitution and matching editor behavior. Dynamic styles, data images, local fonts, and WebGL asset requests must also be checked against the destination policy; copying the review server's broader policy is not an integration plan.

## 6. Preserve the meaning of state and controls

The view has four distinct sources of information:

| Source | Meaning |
| --- | --- |
| Draft target | The current author input resolved by the compiler. |
| Screen frame | A local animation sample. It does not prove execution. |
| Accepted target | A command accepted by a transport or Job; still not an optical measurement. |
| Cached device readback | A service observation with its own sample timestamp and availability. |

The demo emits `nb1-design-report` from its software HID adapter. Replace that connection with an adapter to the real, reviewed acceptance/readback lifecycle. Do not manufacture an accepted event from a screen frame or change a cached device record to match the illustration.

Screen playback must consume the actual compiled Sequence and matching Go planner, including initial state, waits, repeats, intervals, and finish. Do not recreate recipes or color mathematics in JavaScript. Edits and new intentions cancel stale runs; locale, appearance, and camera changes preserve the authored data. Unknown fade origins use the disclosed screen-only assumption. Job submission uncertainty retains the original ID and recovery behavior.

Maintain these presentation decisions:

- Four sizes: `--type-title`, `--type-heading`, `--type-body`, `--type-caption`. Explicit `data-type-role` bindings keep heading semantics independent of visual size.
- Use semantic `--ui-*` color roles. Nightlab has distinct graphite surfaces, ice-blue editing/focus, warm-rose execution, amber progress, and readable effect-specific signals. Theme colors never rewrite canonical LED colors.
- Keep custom menus, option focus/selection, checkboxes, disabled/error states, and an authoritative native-control fallback. Test keyboard operation and long/user-authored labels.
- Default to English with explicit `zh-CN`; do not infer a language from the OS. Preserve user text, JSON, IDs, and canonical values during translation.
- Use action-specific color names: Fade has a target; Alternate/Orbit have A/B and an explicit initial assignment. A/B are not permanent physical LED identities.
- The model title and hint have safe interior spacing and a reserved zoom area. Check both workbenches at 1366, 390, and 320 pixels; a dark border must not clip the first glyph.

## 7. Preserve the approved physical and brand details

- Keep the original approved nb1 mascot, icon, and provenance bytes. `B-3` is an internal artwork selection code, not a public name.
- Fixed on the physically checked MK2: **LED 1 = blink(1) wordmark face; LED 2 = ThingM face**. The USB open half faces blink(1); the black tongue faces ThingM. Camera rotation never swaps them.
- Preserve the continuous two-source light field. Linear contributions blend in the shared diffuser; two independent control channels do not imply a hard two-color surface split. Diagnostic swatches retain original channel values.
- Keep the corrected rounded metal/plastic join. Do not reintroduce negative metal depth bias, coincident exterior ownership, or fixed-Z corner fitting that exposes internal metal as teeth. Inspect USB end-on and quarter views, lit and unlit.
- Preserve bounded zoom, normal page scrolling, keyboard access, finite optional motion, reduced motion, and the loading/no-WebGL/context-loss two-face fallback. `null`/unspecified and `#000000`/off remain different states.

The source STL files, derived display mesh, and visual light field are documented in `model-assets/source.json` and `model-assets/seating-fit.json`. Hidden mounting overlap remains allowed in this illustration; it is not a manufacturing assembly or calibrated optical model. Keep ThingM CAD attribution and the supplied CC-BY-SA-4.0 license, Three.js MIT license, Go/compiler notices, editor notices, and font licenses with reused assets.

## 8. Acceptance and a starter task for the next Agent

In the handoff's `source/`, after dependency setup and a build, the relevant checks are:

```sh
npm run check
npm run test:composer
npm run test:preview
npm run test:appearance
npm run test:fit
npm run test:3d
```

Run the suites appropriate to the files being changed. They exercise software fixtures and local test servers, not the daily daemon. They establish this sample's behavior; rerun the destination project's own protocol, locale, import, CSP, and browser checks after integration. The handoff's reviewed screenshots are visual references, not a substitute for checking the integrated page.

At minimum, verify custom menus and focus; contextual labels; all nine effects; real Rainbow screen animation; pause/continue/stop/replay; stale-response cancellation; locale/theme changes without draft mutation; off/unspecified states; explicit execution semantics; Job recovery; compact desktop/mobile controls; no-WebGL fallback; and the corrected joint at six poses, lit and unlit. Editing, importing, saving, changing language, or previewing must not operate a physical light.

Suggested instruction for a receiving Agent:

> Read this handoff's README, AGENTS, and INTEGRATION guide. Start the local software example and inspect the selected Nightlab desktop and mobile views. Identify the intended current nb1-web and nb1-browser branches, compare them with the recorded baselines, and create an isolated integration worktree. Port semantic tokens, Nightlab controls, and compact layout while preserving the destination's compiler, API, Job recovery, and approved artwork. Establish a lightweight two-face preview first. Propose an explicit asset/CSP/budget plan before adding optional 3D/WASM to the embedded host. Keep simulation code out of production and record software acceptance before any separate hardware qualification.
