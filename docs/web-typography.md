# Embedded Web presentation

The current API v1 workbench is served at `/demo/`, with Orbit and Pattern Book views sharing the same browser controller. Its compact Island desk presentation keeps device selection, authoring, cached observations and the current Job close together. Presentation changes preserve the Action, Sequence, Step, Job and Pattern contracts.

## Language menu

The Language button opens a custom paper-styled listbox instead of an operating-system select popup. English is the first-visit default. The current choices keep the labels **English** and **中文**; additional languages are populated from the shared locale catalog. Explicit selection persists under `nb1.web.locale.v1` in same-origin localStorage. Missing, unsupported or inaccessible preferences fall back to English, while blocked storage still permits a language change on the current page.

Enter/Space opens the menu and selects a focused option. Arrow keys move focus, Home/End reach the first/last option, Escape closes and returns focus, and Tab continues through the page. Outside clicks close the popup while preserving focus on an input or button. Trigger and option targets are at least 44 pixels high, with text at least 14 pixels. The chevron and selected mark are drawn with CSS, independent of font glyphs.

Language changes update presentation only. They preserve user titles, JSON, colors and Job IDs and do not preview, submit or cancel device work.

## Local fonts and Action icons

Fredoka 700 supplies Latin brand and heading shapes. M PLUS Rounded 1c 500/700 supplies Latin body text and controls, using actual static font weights. Four local WOFF2 files and both complete SIL Open Font License 1.1 notices travel with the bundle. The application MIT license does not replace those font notices.

These compact files contain no CJK ideographs. Chinese uses installed native fallback fonts with bold styling; the exact face and available weight depend on the platform. The interface does not claim to ship a Chinese webfont. Code, JSON values and timestamps retain their monospace treatment. No remote font request or system font installation is required.

All nine confirmed Action buttons use original static SVG drawings with visible text labels. The refreshed palette uses warm cream, fresh leaf green, turquoise, coral and yellow accents with deep text. Rainbow has seven separated colored arcs in a 44 × 34 pixel illustration; its display shades do not change the compiler anchors or device RGB values. Orbit uses a warm gold planet with tilted Saturn-like rings. Breathe uses an expansion motif, while Pulse depicts a quick rise and a longer decay. These decorative cues use no Unicode icon font and do not simulate a running effect or establish device completion.

## Build and serving boundary

The refreshed bundle declares **21 asset files plus `manifest.json`: 22 files in total**. The complete payload, including font licenses and provenance, remains bounded at **256 KiB**. The independent `nb1-web` source builds the static files; nb1 imports the reviewed output into `internal/webui/dist` and embeds it in the Go executable. Users need no Node process or frontend development server.

The manifest records the producer source commit, file sizes and SHA-256 values. `internal/webui/manifest.lock.json` binds the imported source and manifest digest. Those records identify the actual bundle; a source or documentation edit alone does not update an installed daemon.

The reviewed import is pinned to:

- Producer source: `04963bff037dc77bab0c526ed7e24a21a2a48d18`.
- Manifest SHA-256: `53a67edd565af2162f3cfbef4404a2858c3eb5a9e962ee517860fcb7fe22ce90`.
- Embedded payload: **199,114 bytes across 22 files**, leaving **63,030 bytes** within the 256 KiB limit.

All runtime assets are local and use the same origin as the API. The Content Security Policy explicitly permits packaged fonts with `font-src 'self'`, while scripts, styles and API connections remain same-origin. The interface adds no CDN, analytics, Service Worker, inline script or additional framework runtime.

## Verification boundary

Software checks cover font delivery, menu keyboard and focus behavior, language persistence, SVG rendering, mobile layout and explicit execution controls. They establish interface behavior. USB operation and physical light quality require separately identified device and optical evidence.

The LED swatches display cached RGB observations with a sample time. Their screen appearance, font rendering and SVG icons do not measure diffuser brightness, fade continuity or flicker. A successful Job confirms its execution and cleanup outcome; optical conclusions remain tied to the recorded hardware, camera conditions and matching core build.
