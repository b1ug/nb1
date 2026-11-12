# Embedded Web presentation

The current API v1 workbench is served at `/demo/`, with Orbit and Pattern Book views sharing the same browser controller. Its compact Island desk presentation keeps device selection, authoring, cached observations and the current Job close together. Presentation changes preserve the Action, Sequence, Step, Job and Pattern contracts.

## Navigation and menus

Page navigation and the Action / Sequence / Pattern editor tabs have separate roles. Workbench, Orbit and Pattern Book are page links with a single `aria-current="page"`; editor tabs select the authoring format within a page. Language selection is a compact secondary control rather than a fourth page tab.

The language control opens a paper-styled listbox. English is the first-visit default. The choices retain their full labels **English** and **中文** even when the closed trigger uses a shorter language label. Additional languages are populated from the shared locale catalog. Explicit selection persists under `nb1.web.locale.v1` in same-origin localStorage. Missing, unsupported or inaccessible preferences fall back to English, while blocked storage still permits a language change on the current page.

Enter/Space opens the menu and selects a focused option. Arrow keys move focus, Home/End reach the first/last option, Escape closes and returns focus, and Tab continues through the page. Outside clicks close the popup while preserving focus on an input or button. Trigger and option targets are at least 44 pixels high, with text at least 14 pixels. The chevron and selected mark are drawn with CSS, independent of font glyphs.

Language changes update presentation only. They preserve user titles, JSON, colors and Job IDs and do not preview, submit or cancel device work.

The device control keeps the selected serial and connection state separate from navigation. Its menu uses the workbench's paper surface and visible focus styling. Choosing a device may read its cached state; opening a menu, changing language or navigating between pages does not submit playback.

## Local character and favicon

Two explicitly reviewed image assets travel with the bundle: `assets/nb1-icon.svg` for the favicon and `assets/nb1-mascot.webp` for the small brand illustration. The character is decorative; it is not a physical diagram, readback swatch, animation preview or connection indicator. Connected-device state remains in the labeled device controls and observations.

The image routes are exact: `/demo/assets/nb1-icon.svg` returns `image/svg+xml`, and `/demo/assets/nb1-mascot.webp` returns `image/webp`. No image directory listing, arbitrary filename or remote-image proxy is exposed. Both files retain the same manifest hashes, size budget, no-store and nosniff checks as the other static resources.

## Local fonts and Action icons

Fredoka 700 supplies Latin brand and heading shapes. M PLUS Rounded 1c 500/700 supplies Latin body text and controls, using actual static font weights. Four local WOFF2 files and both complete SIL Open Font License 1.1 notices travel with the bundle. The application MIT license does not replace those font notices.

These compact files contain no CJK ideographs. Chinese uses installed native fallback fonts with bold styling; the exact face and available weight depend on the platform. The interface does not claim to ship a Chinese webfont. Code, JSON values and timestamps retain their monospace treatment. No remote font request or system font installation is required.

All nine confirmed Action buttons use original static SVG drawings with visible text labels. The palette now follows a quieter New Horizons-inspired hierarchy: cream paper, wood-brown text and pale sage, with small sea-green, warm-yellow and coral accents. All Action tiles share a paper surface; color is concentrated in their small original icon stamps and selection state. This is our interpretation of Nintendo’s game UI and official site, without bundled Nintendo artwork. Rainbow has seven separated colored arcs in a 44 × 34 pixel illustration; its display shades do not change the compiler anchors or device RGB values. Orbit uses a warm gold planet with tilted Saturn-like rings. Breathe uses an expansion motif, while Pulse depicts a quick rise and a longer decay. These decorative cues use no Unicode icon font and do not simulate a running effect or establish device completion.

## Build and serving boundary

The refreshed asset contract declares **23 asset files plus `manifest.json`: 24 files in total**. The complete payload, including image assets, font licenses and provenance, remains bounded at **256 KiB**. The independent `nb1-web` source builds the static files; nb1 imports the reviewed output into `internal/webui/dist` and embeds it in the Go executable. Users need no Node process or frontend development server.

The manifest records the producer source commit, file sizes and SHA-256 values. `internal/webui/manifest.lock.json` binds the imported source and manifest digest. Those records identify the actual bundle; a source or documentation edit alone does not update an installed daemon.

Read the selected producer commit and manifest digest from `internal/webui/manifest.lock.json`; per-file sizes and hashes are in the accompanying `dist/manifest.json`. These generated records identify the imported bundle, including its exact total size. A source or documentation edit alone does not update those records or an installed daemon.

All runtime assets are local and use the same origin as the API. The Content Security Policy explicitly permits packaged fonts with `font-src 'self'` and images with `img-src 'self'`, while scripts, styles and API connections remain same-origin. Images do not gain permission for external hosts or `data:` sources. The interface adds no CDN, analytics, Service Worker, inline script or additional framework runtime.

## Verification boundary

Software checks cover font delivery, menu keyboard and focus behavior, language persistence, SVG rendering, mobile layout and explicit execution controls. They establish interface behavior. USB operation and physical light quality require separately identified device and optical evidence.

The LED swatches display cached RGB observations with a sample time. Their screen appearance, font rendering and SVG icons do not measure diffuser brightness, fade continuity or flicker. A successful Job confirms its execution and cleanup outcome; optical conclusions remain tied to the recorded hardware, camera conditions and matching core build.
