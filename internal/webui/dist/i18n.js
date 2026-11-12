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
  const control = document.querySelector(".locale-control");
  const trigger = document.getElementById("locale-select");
  const currentLabel = document.getElementById("locale-current");
  const languageLabel = document.getElementById("locale-label");
  const shortLabel = document.getElementById("locale-short");
  const listbox = document.getElementById("locale-options");
  // Current locale names stay familiar; future catalog entries use their native label.
  const localeName = code => ({en: "English", "zh-CN": "中文"})[code] || catalog.locales[code].label;
  const codes = Object.keys(catalog.locales);
  const options = [];
  let menuOpen = false, activeIndex = 0;
  function focusOption(index) {
    activeIndex = (index + codes.length) % codes.length;
    options[activeIndex].focus({preventScroll: true});
  }
  function closeMenu(returnFocus = false) {
    menuOpen = false;
    listbox.hidden = true;
    trigger.setAttribute("aria-expanded", "false");
    control.classList.remove("is-open");
    if (returnFocus) trigger.focus({preventScroll: true});
  }
  function openMenu(index = codes.indexOf(locale)) {
    document.dispatchEvent(new CustomEvent("nb1-menu-open", {detail: control}));
    menuOpen = true;
    listbox.hidden = false;
    trigger.setAttribute("aria-expanded", "true");
    control.classList.add("is-open");
    focusOption(index < 0 ? 0 : index);
  }
  function chooseLocale(code) {
    locale = supported(code) ? code : fallback;
    try { localStorage.setItem(preferenceKey, locale); } catch (_) { /* This page still switches. */ }
    refresh();
    closeMenu(true);
  }
  for (const [index, code] of codes.entries()) {
    const option = document.createElement("li");
    option.id = "locale-option-" + index;
    option.className = "locale-option";
    option.setAttribute("role", "option");
    option.setAttribute("lang", code);
    option.setAttribute("tabindex", "-1");
    option.dataset.locale = code;
    const label = document.createElement("span"); label.textContent = localeName(code);
    const mark = document.createElement("span"); mark.className = "locale-check"; mark.setAttribute("aria-hidden", "true");
    option.append(label, mark);
    option.addEventListener("click", () => chooseLocale(code));
    option.addEventListener("focus", () => { activeIndex = index; });
    options.push(option); listbox.append(option);
  }
  trigger.addEventListener("click", () => { if (menuOpen) closeMenu(true); else openMenu(); });
  trigger.addEventListener("keydown", event => {
    if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
      event.preventDefault();
      openMenu(event.key === "Home" ? 0 : event.key === "End" ? codes.length - 1 : codes.indexOf(locale));
    } else if (event.key === "Escape" && menuOpen) { event.preventDefault(); closeMenu(true); }
  });
  listbox.addEventListener("keydown", event => {
    if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
      event.preventDefault();
      focusOption(event.key === "Home" ? 0 : event.key === "End" ? codes.length - 1 : activeIndex + (event.key === "ArrowDown" ? 1 : -1));
    } else if (event.key === "Enter" || event.key === " ") {
      event.preventDefault(); chooseLocale(codes[activeIndex]);
    } else if (event.key === "Escape") { event.preventDefault(); closeMenu(true); }
    else if (event.key === "Tab") {
      // Continue from the trigger's place in page tab order, outside the popup.
      closeMenu(true);
    }
  });
  document.addEventListener("click", event => {
    if (!menuOpen || control.contains(event.target)) return;
    // Preserve focus on an outside input/button; return it after a background click.
    const active = document.activeElement;
    closeMenu(active === document.body || listbox.contains(active));
  });
  control.addEventListener("focusout", event => {
    if (menuOpen && event.relatedTarget && !control.contains(event.relatedTarget)) closeMenu();
  });
  document.addEventListener("nb1-menu-open", event => { if (menuOpen && event.detail !== control) closeMenu(false); });
  const observer = new MutationObserver(() => refresh());
  function refresh() {
    observer.disconnect();
    const walker = document.createTreeWalker(document.documentElement, NodeFilter.SHOW_TEXT);
    let node; while ((node = walker.nextNode())) textNode(node);
    for (const element of document.querySelectorAll('[aria-label], [title], [placeholder]')) for (const name of ["aria-label", "title", "placeholder"]) attributeNode(element, name);
    document.documentElement.lang = locale;
    languageLabel.textContent = localized("Language");
    currentLabel.textContent = localeName(locale);
    shortLabel.textContent = ({en: "EN", "zh-CN": "中文"})[locale] || locale.toUpperCase();
    for (const [index, code] of codes.entries()) options[index].setAttribute("aria-selected", String(code === locale));
    observer.observe(document.documentElement, {subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ["aria-label", "title", "placeholder", "data-user-title"]});
  }
  Object.defineProperty(window, "nb1Locale", {value: Object.freeze({getLocale: () => locale, preferenceKey}), writable: false});
  refresh();
})();
