"use strict";

// Presentation only. This module never calls page renderers or the network.
(() => {
  const catalog = window.nb1LocaleCatalog;
  const preferenceKey = "nb1.web.locale.v1";
  const own = (value, key) => Object.prototype.hasOwnProperty.call(value, key);
  const fallback = "en";
  const supported = code => typeof code === "string" && own(catalog.locales, code);
  let locale = fallback;
  try { const saved = localStorage.getItem(preferenceKey); if (supported(saved)) locale = saved; } catch (_) { /* English without storage. */ }
  const originals = new WeakMap(), attributes = new WeakMap();
  const cache = new Map();
  const hash = text => {
    let a = 2166136261, b = 5381;
    for (let i = 0; i < text.length; i++) { const c = text.charCodeAt(i); a = Math.imul(a ^ c, 16777619); b = Math.imul(b, 33) ^ c; }
    return btoa(String.fromCharCode(a >>> 24, a >>> 16 & 255, a >>> 8 & 255, a & 255, b >>> 24, b >>> 16 & 255, b >>> 8 & 255, b & 255)).replace(/\+/g, "-").replace(/\//g, "_").slice(0, 11);
  };
  const escape = text => text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const templates = catalog.templates.map(([english, key, recursive = 0]) => {
    const indices = []; let end = 0, expression = "^";
    for (const match of english.matchAll(/\{p(\d+)\}/g)) { expression += escape(english.slice(end, match.index)) + "([\\s\\S]*?)"; indices.push(Number(match[1])); end = match.index + match[0].length; }
    expression += escape(english.slice(end)) + "$";
    return {english, key, recursive, indices, match: new RegExp(expression), weight: english.replace(/\{p\d+\}/g, "").length};
  }).sort((a, b) => b.weight - a.weight);
  function translate(text, depth = 0) {
    if (locale === fallback || !text || depth > 4) return text;
    const messages = catalog.locales[locale].messages;
    const exact = messages[hash(text)];
    if (typeof exact === "string") return exact;
    const trimmed = text.trim();
    if (trimmed !== text && typeof messages[hash(trimmed)] === "string") return text.slice(0, text.indexOf(trimmed)) + messages[hash(trimmed)] + text.slice(text.indexOf(trimmed) + trimmed.length);
    for (const item of templates) {
      if (!own(messages, item.key)) continue;
      const match = item.match.exec(text); if (!match) continue;
      const values = [];
      item.indices.forEach((index, capture) => { values[index] = item.recursive & (1 << index) ? translate(match[capture + 1], depth + 1) : match[capture + 1]; });
      return messages[item.key].replace(/\{p(\d+)\}/g, (_, i) => values[Number(i)]);
    }
    return text;
  }
  function localized(source) {
    const key = locale + "\0" + source;
    if (!cache.has(key)) { if (cache.size > 2000) cache.clear(); cache.set(key, translate(source)); }
    return cache.get(key);
  }
  function rawElement(element) {
    return element.closest('[data-i18n-skip], script, style, textarea, code, #serial, #product, #job-id, #rgb-one, #rgb-two, #pattern-list strong, #selected-pattern[data-user-title]');
  }
  function textNode(node) {
    const parent = node.parentElement;
    if (!parent || rawElement(parent)) return;
    const saved = originals.get(node);
    const source = saved && node.data === saved.rendered ? saved.source : node.data;
    // JSON/transport/preformatted data is original. Only known initial help may change.
    const pre = parent.closest("pre");
    if (pre && (!pre.hasAttribute("data-i18n-help") || /^[\s]*[\[{]/.test(source) || !own(catalog.locales["zh-CN"].messages, hash(source)))) return;
    if (parent.closest("option") && !parent.closest("option").hasAttribute("value")) return;
    const rendered = localized(source);
    originals.set(node, {source, rendered});
    if (node.data !== rendered) node.data = rendered;
  }
  function attributeNode(element, name) {
    if (element.closest('[data-i18n-skip]')) return;
    const current = element.getAttribute(name); if (current === null) return;
    let saved = attributes.get(element); if (!saved) attributes.set(element, saved = {});
    const old = saved[name]; const source = old && current === old.rendered ? old.source : current;
    // Swatches expose both a human label and unchanged API name/hex value.
    const color = name === "aria-label" && element.classList.contains("color-swatch") && /^([A-Z][a-z]+)( [a-z]+ #[0-9a-f]{6})$/.exec(source);
    const swatch = name === "title" && element.matches(".color-swatch, .palette button") && /^([A-Za-z]+)([ ·]+#[0-9a-f]{6})$/.exec(source);
    const label = swatch && localized(swatch[1][0].toUpperCase() + swatch[1].slice(1));
    const rendered = color ? localized(color[1]) + color[2] : swatch && locale !== fallback ? label + (swatch[1] === swatch[1].toLowerCase() ? " · " + source : swatch[2]) : localized(source); saved[name] = {source, rendered};
    if (current !== rendered) element.setAttribute(name, rendered);
  }
  const observer = new MutationObserver(() => refresh());
  function refresh() {
    observer.disconnect();
    const walker = document.createTreeWalker(document.documentElement, NodeFilter.SHOW_TEXT);
    let node; while ((node = walker.nextNode())) textNode(node);
    for (const element of document.querySelectorAll('[aria-label], [title], [placeholder]')) for (const name of ["aria-label", "title", "placeholder"]) attributeNode(element, name);
    document.documentElement.lang = locale;
    document.getElementById("locale-select").value = locale;
    observer.observe(document.documentElement, {subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ["aria-label", "title", "placeholder", "data-user-title"]});
  }
  const select = document.getElementById("locale-select");
  // Locale names identify the choices in their own languages.
  select.setAttribute("data-i18n-skip", "");
  for (const [code, value] of Object.entries(catalog.locales)) {
    if (![...select.options].some(option => option.value === code)) { const option = document.createElement("option"); option.value = code; option.lang = code; option.textContent = value.label; select.append(option); }
  }
  select.addEventListener("change", () => {
    locale = supported(select.value) ? select.value : fallback;
    try { localStorage.setItem(preferenceKey, locale); } catch (_) { /* The current page still switches. */ }
    refresh();
  });
  Object.defineProperty(window, "nb1Locale", {value: Object.freeze({getLocale: () => locale, preferenceKey}), writable: false});
  refresh();
})();
