# Embedded Web presentation

The current API v1 workbench is served at `/demo/`, with Orbit and Pattern Book views sharing the same browser controller. Its compact Island desk presentation keeps device selection, authoring, cached observations and the current Job close together. Presentation changes preserve the Action, Sequence, Step, Job and Pattern contracts.

## Navigation and menus

The header keeps the brand and a compact language utility. Repeated Workbench / Orbit / Pattern Book shortcuts have been removed: Action / Sequence / Pattern tabs and the Action tiles already provide the authoring choices in the workspace. The existing page URLs still select their initial view; no extra shortcut row is needed.

The language control opens a paper-styled listbox. English is the first-visit default. The choices retain their full labels **English** and **中文** even when the closed trigger uses a shorter language label. Additional languages are populated from the shared locale catalog. Explicit selection persists under `nb1.web.locale.v1` in same-origin localStorage. Missing, unsupported or inaccessible preferences fall back to English, while blocked storage still permits a language change on the current page.

Enter/Space opens the menu and selects a focused option. Arrow keys move focus, Home/End reach the first/last option, Escape closes and returns focus, and Tab continues through the page. Outside clicks close the popup while preserving focus on an input or button. Trigger and option targets are at least 44 pixels high, with text at least 14 pixels. The chevron and selected mark are drawn with CSS, independent of font glyphs.

Language changes update presentation only. They preserve user titles, JSON, colors and Job IDs and do not preview, submit or cancel device work.

Single-select dropdowns share one paper-styled presentation, including the selected device, saved Pattern, color format and dynamically created Action parameters. Their original select elements remain the source of values and change events; opening a menu or moving focus does not change the author input. Only a committed different value sends the existing change event. A device change may read cached state, while a Pattern choice still needs the explicit Read Pattern action. Refresh and language changes update menu labels without altering serials, Pattern titles, canonical values or execution behavior. Local color choices are presented beside their associated color input; the native color picker retains its own interaction.

## Local colors and Patterns

Local color storage stays with the color being edited. Each Action color card has its own saved-color selector, source sample and nearby naming/save controls. Selecting a saved color applies it only to that card; it keeps the current Action and the other color unchanged. The save source identifies the selected card and its authored color or resolved Hex value, so users do not have to find a distant input to understand what will be saved. The Sequence editor has a nearby local-color reference control for copying an `@name` without switching to Action or rewriting the JSON.

Pattern saving is an open compact card beside the authoring area on wide screens. On narrow screens it follows the editor before device observations and playback details. It identifies the current Action or Sequence as its source. The Pattern input tab remains the place to choose, read and edit an existing Pattern, with its refresh control beside that choice. There are no separate color/Pattern storage drawers at the foot of the page. Editing a save name, Pattern ID or title does not alter the lighting draft or invalidate a valid Preview.

A save captures its source input and destination name/ID when clicked. Pending saves stay guarded against duplicate clicks even while the author continues editing. Results identify that captured source; they do not imply a later draft or color was saved. Saving a color fixes its resolved RGB value, while saving a Pattern fixes its compiled Sequence on this computer. Neither operation creates a Job or changes the lamp. After an uncertain save outcome, the interface blocks another save until an explicit readback of the captured destination. Readback updates its receipt without replacing the active draft, save metadata or Preview. The interface does not automatically retry.

## Local character and favicon

The approved nb1 character is used consistently for the workbench, documentation and favicon. Two production derivatives of that same character travel with the bundle: `assets/nb1-icon.png` for the favicon and `assets/nb1-mascot.webp` for the small brand illustration. They preserve the approved character rather than using separate mascot designs. Source artwork and usage guidance are maintained in the independent nb1-web repository; only the production derivatives are embedded here. The character is decorative; it is not a physical diagram, readback swatch, animation preview or connection indicator. Connected-device state remains in the labeled device controls and observations.

The image routes are exact: `/demo/assets/nb1-icon.png` returns `image/png`, and `/demo/assets/nb1-mascot.webp` returns `image/webp`. No image directory listing, arbitrary filename or remote-image proxy is exposed. Both files retain the same manifest hashes, size budget, no-store and nosniff checks as the other static resources.

## Local fonts and Action icons

Fredoka 700 supplies Latin brand and heading shapes. M PLUS Rounded 1c 500/700 supplies Latin body text and controls, using actual static font weights. Four local WOFF2 files and both complete SIL Open Font License 1.1 notices travel with the bundle. The application MIT license does not replace those font notices.

These compact files contain no CJK ideographs. Chinese uses installed native fallback fonts with bold styling; the exact face and available weight depend on the platform. The interface does not claim to ship a Chinese webfont. Code, JSON values and timestamps retain their monospace treatment. No remote font request or system font installation is required.

All nine confirmed Action buttons use original static SVG drawings with visible text labels. The palette now follows a quieter New Horizons-inspired hierarchy: cream paper, wood-brown text and pale sage, with small sea-green, warm-yellow and coral accents. All Action tiles share a paper surface; color is concentrated in their small original icon stamps and selection state. This is our interpretation of Nintendo’s game UI and official site, without bundled Nintendo artwork. Rainbow has seven separated colored arcs in a 44 × 34 pixel illustration; its display shades do not change the compiler anchors or device RGB values. Orbit uses a warm gold planet with tilted Saturn-like rings. Breathe uses an expansion motif, while Pulse depicts a quick rise and a longer decay. These decorative cues use no Unicode icon font and do not simulate a running effect or establish device completion.

## Build and serving boundary

The refreshed asset contract declares **26 asset files plus `manifest.json`: 27 files in total**. The complete payload, including image assets, font licenses and provenance, remains bounded at **640 KiB**. The independent `nb1-web` source builds the static files; nb1 imports the reviewed output into `internal/webui/dist` and embeds it in the Go executable. Users need no Node process or frontend development server.

The `static-editor/v1` manifest records the producer source commit, file sizes and SHA-256 values. The reviewed `json-editor.js` bundle, `json-editor.css` and complete `editor-LICENSES.txt` are included in the same closed asset inventory; the JavaScript uses packaged CodeMirror 6 modules with no runtime package fetch, dynamic import or evaluation. Their exact routes return JavaScript, CSS and plain text respectively. `internal/webui/manifest.lock.json` binds the imported source and manifest digest. Those records identify the actual bundle; a source or documentation edit alone does not update an installed daemon.

Read the selected producer commit and manifest digest from `internal/webui/manifest.lock.json`; per-file sizes and hashes are in the accompanying `dist/manifest.json`. These generated records identify the imported bundle, including its exact total size. A source or documentation edit alone does not update those records or an installed daemon.

All runtime assets are local and use the same origin as the API. The Content Security Policy explicitly permits packaged fonts with `font-src 'self'` and images with `img-src 'self'`, while scripts and API connections remain same-origin. Images do not gain permission for external hosts or `data:` sources. The interface adds no CDN, analytics, Service Worker or inline script.

### Response style nonces

CodeMirror installs its generated style modules as style elements. Each of the three HTML templates contains exactly one canonical `<meta name="nb1-style-nonce" content="__NB1_STYLE_NONCE__">`. The verified, embedded source keeps that placeholder, so its manifest and source-tree digests stay reproducible. Serving an HTML request makes a response copy and replaces only that meta's placeholder with 24 bytes from `crypto/rand`, encoded as Base64. The response's CSP changes only `style-src`, adding the matching `'nonce-…'` value alongside `'self'`; the editor passes this value to CodeMirror's style-module support. No `unsafe-inline` or `unsafe-eval` permission is added.

The nonce is fresh for every HTML response, including repeated requests for the same page. It is not a device identifier, Job ID, API credential or persisted setting. Static JavaScript, CSS, font, image and license responses retain their exact manifest bytes and the original same-origin CSP without a nonce. HTML response bytes differ from the pinned template only at the canonical meta placeholder. `Cache-Control: no-store` applies throughout.

A missing or repeated meta, another occurrence of the placeholder or nonce name, a noncanonical meta, or unavailable random bytes causes HTTP 500 with a JSON error before any HTML is written. The server does not send an unprotected fallback page. It never rewrites the bundled files or their lock during a request. A plain static developer preview does not establish this daemon-specific nonce and CSP behavior; use the matching nb1 host to validate the protected embedded editor.

## Verification boundary

Software checks cover font delivery, menu keyboard and focus behavior, language persistence, SVG rendering, mobile layout and explicit execution controls. They establish interface behavior. USB operation and physical light quality require separately identified device and optical evidence.

The LED swatches display cached RGB observations with a sample time. Their screen appearance, font rendering and SVG icons do not measure diffuser brightness, fade continuity or flicker. A successful Job confirms its execution and cleanup outcome; optical conclusions remain tied to the recorded hardware, camera conditions and matching core build.

## JSON color samples

Authored Sequence targets use the same ColorInput grammar as Action colors: Hex3/Hex6, fixed names, RGB8/HSV objects and local aliases. The editor resolves color samples through the local Go color endpoint, so it does not rely on the browser's broader CSS color grammar. A sample only decorates the source; it preserves authored JSON, numeric spelling and Undo history. Color resolution reads no hardware and creates no Job. Preview, playback and Pattern saving resolve the same authored colors into exact lowercase Hex6 values.
