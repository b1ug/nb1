/* UI appearance only. This module never reads or changes drafts, LED targets,
   compiled programs, preview transports or the 3D camera. No stored preference. */
(() => {
  'use strict';
  const root = document.documentElement;
  const skins = ['airwave', 'instrument', 'nightlab'];
  const requested = new URL(location.href).searchParams.get('skin');
  if (!root.dataset.studioSkin) root.dataset.studioSkin = skins.includes(requested) ? requested : 'nightlab';
  function init() {
    if (document.querySelector('[data-studio-appearance]')) return;
    const host = document.querySelector('header.workbench-topbar, header nav.wrap');
    if (!host) return;
    const languageControl = host.querySelector('.locale-control, #language');
    const copy = {
      en: {
        appearance: 'Appearance', menu: 'Workbench appearance', current: 'Current appearance',
        airwave: 'Airy blue, white and warm rose',
        instrument: 'Warm white, aluminum and precise controls',
        nightlab: 'Graphite surfaces with clear colored signals',
      },
      'zh-CN': {
        appearance: '外观', menu: '工作台外观', current: '当前外观',
        airwave: '轻盈蓝白，淡暖粉点睛',
        instrument: '暖白铝灰，精密仪器分区',
        nightlab: '黑石墨层次，清楚的彩色信号',
      },
    };
    const names = { airwave: 'Airwave', instrument: 'Instrument', nightlab: 'Nightlab' };
    const box = document.createElement('div');
    box.className = 'studio-appearance'; box.dataset.studioAppearance = ''; box.setAttribute('data-i18n-skip', '');
    const trigger = document.createElement('button');
    trigger.id = 'studio-appearance-trigger'; trigger.className = 'studio-appearance-trigger'; trigger.type = 'button';
    trigger.setAttribute('aria-haspopup', 'menu'); trigger.setAttribute('aria-expanded', 'false'); trigger.setAttribute('aria-controls', 'studio-appearance-menu');
    trigger.innerHTML = '<svg viewBox="0 0 20 20" width="18" height="18" aria-hidden="true" focusable="false"><rect x="2.5" y="3" width="15" height="14" rx="2" fill="none" stroke="currentColor" stroke-width="1.4"/><path d="M2.5 7.5h15M8 7.5V17" fill="none" stroke="currentColor" stroke-width="1.4"/></svg><span class="studio-appearance-current"></span><svg class="studio-appearance-chevron" viewBox="0 0 12 12" width="12" height="12" aria-hidden="true"><path d="m3 4.5 3 3 3-3" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/></svg>';
    const current = trigger.querySelector('.studio-appearance-current');
    const menu = document.createElement('div');
    menu.id = 'studio-appearance-menu'; menu.className = 'studio-appearance-menu'; menu.setAttribute('role', 'menu'); menu.hidden = true;
    const choices = skins.map(skin => {
      const button = document.createElement('button'); button.type = 'button'; button.className = 'studio-appearance-choice'; button.dataset.skinChoice = skin;
      button.setAttribute('role', 'menuitemradio'); button.setAttribute('aria-checked', 'false'); button.tabIndex = -1;
      button.innerHTML = `<span class="studio-appearance-swatch" data-swatch-skin="${skin}" aria-hidden="true"><i></i><i></i><i></i></span><span class="studio-appearance-copy"><strong>${names[skin]}</strong><small></small></span><svg class="studio-appearance-check" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true"><path d="m3 8 3 3 7-7" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/></svg>`;
      button.addEventListener('click', () => {
        root.dataset.studioSkin = skin;
        // Keep the comparison link shareable without reloading or touching app state.
        try {
          const url = new URL(location.href);
          if (skin === 'nightlab') url.searchParams.delete('skin'); else url.searchParams.set('skin', skin);
          history.replaceState(history.state, '', url);
        } catch { /* A file preview may restrict history updates; appearance still works. */ }
        paint(); close(true);
      });
      menu.append(button); return button;
    });
    box.append(trigger, menu);
    if (languageControl) host.insertBefore(box, languageControl); else host.append(box);
    function setText(node, value) { if (node.textContent !== value) node.textContent = value; }
    function setAttribute(node, name, value) { if (node.getAttribute(name) !== value) node.setAttribute(name, value); }
    function paint() {
      const words = copy[root.lang === 'zh-CN' ? 'zh-CN' : 'en'];
      const selected = skins.includes(root.dataset.studioSkin) ? root.dataset.studioSkin : 'nightlab';
      setText(current, names[selected]);
      setAttribute(trigger, 'aria-label', `${words.appearance}: ${names[selected]}`);
      setAttribute(trigger, 'title', `${words.appearance}: ${names[selected]}`);
      setAttribute(menu, 'aria-label', words.menu);
      for (const button of choices) {
        const skin = button.dataset.skinChoice;
        setText(button.querySelector('small'), words[skin]);
        setAttribute(button, 'aria-checked', String(skin === selected));
        setAttribute(button, 'aria-label', `${names[skin]} · ${words[skin]}${skin === selected ? ` · ${words.current}` : ''}`);
      }
    }
    function focusChoice(index) {
      const next = (index + choices.length) % choices.length;
      choices.forEach((button, at) => { button.tabIndex = at === next ? 0 : -1; });
      choices[next].focus({ preventScroll: true });
    }
    function close(returnFocus = false) {
      menu.hidden = true; setAttribute(trigger, 'aria-expanded', 'false');
      choices.forEach(button => { button.tabIndex = -1; });
      if (returnFocus) trigger.focus({ preventScroll: true });
    }
    function open(index = skins.indexOf(root.dataset.studioSkin)) {
      document.dispatchEvent(new CustomEvent('nb1-menu-open', { detail: box }));
      menu.style.removeProperty('--appearance-menu-shift');
      menu.hidden = false; setAttribute(trigger, 'aria-expanded', 'true');
      const rect = menu.getBoundingClientRect();
      const shift = rect.left < 16 ? 16 - rect.left : rect.right > innerWidth - 16 ? innerWidth - 16 - rect.right : 0;
      if (shift) menu.style.setProperty('--appearance-menu-shift', `${shift}px`);
      focusChoice(index < 0 ? 0 : index);
    }
    trigger.addEventListener('click', () => menu.hidden ? open() : close(true));
    trigger.addEventListener('keydown', event => {
      if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
      event.preventDefault(); open(event.key === 'ArrowUp' || event.key === 'End' ? choices.length - 1 : 0);
    });
    menu.addEventListener('keydown', event => {
      const index = choices.indexOf(document.activeElement);
      if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); close(true); }
      else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); focusChoice(index + (event.key === 'ArrowDown' ? 1 : -1)); }
      else if (event.key === 'Home' || event.key === 'End') { event.preventDefault(); focusChoice(event.key === 'Home' ? 0 : choices.length - 1); }
      else if (event.key === 'Tab') setTimeout(() => close(false), 0);
    });
    box.addEventListener('focusout', () => setTimeout(() => { if (!box.contains(document.activeElement)) close(false); }, 0));
    document.addEventListener('pointerdown', event => { if (!box.contains(event.target)) close(false); });
    window.addEventListener('resize', () => close(false));
    document.addEventListener('nb1-menu-open', event => { if (event.detail !== box) close(false); });
    new MutationObserver(paint).observe(root, { attributes: true, attributeFilter: ['lang'] });
    paint();
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init, { once: true }); else init();
})();
