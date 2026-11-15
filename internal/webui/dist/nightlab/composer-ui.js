/* Human-facing names only. The original form values, compiler and playback own behavior. */
(() => {
  'use strict';
  const browser = document.documentElement.dataset.nb1App === 'browser';
  const composer = document.querySelector('.composer');
  if (!composer) return;
  // UI-local lamp recipes; no compiler, saved Pattern or execution behavior.
  const sets = [{"id":"essentials","label":{"en":"Essentials","zh-CN":"基础"},"character":{"en":"Common colors","zh-CN":"常用颜色"},"colors":["#ff0000","#ffa500","#ffff00","#00ff00","#00a060","#00ffff","#0000ff","#8f00ff","#ffffff"]},{"id":"candlelight","label":{"en":"Afterglow","zh-CN":"晚霞余晖"},"character":{"en":"Sunset amber, night blue and rose","zh-CN":"琥珀、夜蓝与玫红的冷暖交错"},"colors":["#c86404","#1434a0","#c00000","#c0a008","#7424a8","#ac286c"]},{"id":"tide","label":{"en":"Sea Breeze","zh-CN":"海风轻拂"},"character":{"en":"Sea blues, sand gold and coral","zh-CN":"海蓝、沙金与珊瑚红"},"colors":["#0090a8","#b87408","#123ca8","#24703c","#9c2440","#6844b0"]},{"id":"petals","label":{"en":"Garden Song","zh-CN":"花间小调"},"character":{"en":"Rose, leaf green and violet accents","zh-CN":"玫红、叶绿与紫色相映"},"colors":["#984064","#30784c","#644c98","#b09808","#246c98","#a44418"]},{"id":"neon","label":{"en":"City Lights","zh-CN":"霓虹夜色"},"character":{"en":"Bright pink, cyan and electric accents","zh-CN":"桃红、青与鲜明亮色"},"colors":["#c00068","#00a0b0","#80b000","#6000c0","#c04800","#143cc0"]},{"id":"forest","label":{"en":"Woodland Glow","zh-CN":"林间微光"},"character":{"en":"Leaf, stream and autumn red colors","zh-CN":"叶绿、溪水青与秋叶红"},"colors":["#387c18","#a86c10","#006868","#88385c","#204c94","#a00000"]},{"id":"dusk","label":{"en":"Blue Hour","zh-CN":"蓝调时分"},"character":{"en":"Indigo shadows and warm points of light","zh-CN":"靛蓝暮色与暖色光点"},"colors":["#2438a0","#b06810","#087c80","#9c2050","#6834a0","#648c18"]},{"id":"citrus","label":{"en":"Summer Orchard","zh-CN":"夏日果园"},"character":{"en":"Fruit colors with green and blue accents","zh-CN":"果实暖色与青绿蓝色"},"colors":["#c45408","#30a050","#b81c38","#bca808","#7430ac","#086ca0"]},{"id":"aurora","label":{"en":"Aurora","zh-CN":"极光流转"},"character":{"en":"Emerald, magenta and blue in motion","zh-CN":"翠绿、洋红与蓝色流转"},"colors":["#00a000","#ac2078","#2050bc","#a88c04","#6030a8","#00a4a4"]}];
  for (const set of sets) { Object.freeze(set.label); Object.freeze(set.character); Object.freeze(set.colors); Object.freeze(set); }
  Object.freeze(sets);
  let selectedId = 'essentials';
  const getSet = id => sets.find(set => set.id === id);
  const chooser = document.createElement('details'); chooser.id = 'color-sets'; chooser.className = 'color-sets'; chooser.setAttribute('data-i18n-skip', '');
  const summary = document.createElement('summary'), heading = document.createElement('span'), selectedName = document.createElement('span'); selectedName.id = 'color-set-name'; summary.append(heading, selectedName);
  const grid = document.createElement('div'); grid.className = 'color-set-grid';
  const cards = sets.map(set => {
    const button = document.createElement('button'); button.type = 'button'; button.className = 'color-set-card'; button.dataset.colorSet = set.id;
    const label = document.createElement('span'), strip = document.createElement('span'); label.className = 'color-set-label'; strip.className = 'color-set-strip'; strip.setAttribute('aria-hidden', 'true');
    for (const color of set.colors) { const swatch = document.createElement('span'); swatch.style.backgroundColor = color; strip.append(swatch); }
    button.append(label, strip); button.addEventListener('click', () => { selectedId = set.id; paint(); document.dispatchEvent(new CustomEvent('nb1-color-set-browse', {detail: {id: set.id}})); }); grid.append(button);
    return {set, button, label};
  });
  const setActions = document.createElement('div'); setActions.className = 'color-set-actions';
  const pairButton = document.createElement('button'), sequenceButton = document.createElement('button'); pairButton.id = 'color-set-pair'; sequenceButton.id = 'color-set-sequence';
  for (const [button, event] of [[pairButton, 'nb1-color-set-pair'], [sequenceButton, 'nb1-color-set-sequence']]) { button.type = 'button'; button.className = 'secondary'; button.addEventListener('click', () => document.dispatchEvent(new CustomEvent(event, {detail: {id: selectedId}}))); }
  setActions.append(pairButton, sequenceButton); chooser.append(summary, grid, setActions);
  window.NB1ColorSets = Object.freeze({sets, get: getSet, get selectedId() { return selectedId; }, refresh: schedule,
    sequence(id) { const set = getSet(id); if (!set) throw Error('Unknown color set'); return {timing: 'serial', steps: set.colors.map(color => ({targets: [{led: 0, color}], fade_ms: 600, hold_ms: 400})), repeat: 3, on_finish: 'off'}; }
  });
  function colorSetsPaint(locale, count) {
    const target = browser ? document.querySelector('#action-fields > .color-field') : composer.querySelector('.color-pair');
    const host = browser ? document.getElementById('action-fields') : target?.parentElement;
    if (host && (chooser.parentElement !== host || target && chooser.nextSibling !== target)) host.insertBefore(chooser, target || null);
    const hidden = count === 0; if (chooser.hidden !== hidden) chooser.hidden = hidden;
    if (pairButton.hidden !== (count !== 2)) pairButton.hidden = count !== 2;
    text(heading, locale === 'zh-CN' ? '配色组' : 'Color sets'); text(selectedName, getSet(selectedId).label[locale]);
    text(pairButton, locale === 'zh-CN' ? '应用双色' : 'Use pair'); text(sequenceButton, locale === 'zh-CN' ? '创建序列' : 'Create Sequence');
    for (const {set, button, label} of cards) { text(label, set.label[locale]); attr(button, 'title', set.character[locale]); attr(button, 'aria-pressed', String(set.id === selectedId)); }
  }
  const copy = {
    en: {
      controls: 'Light controls', effects: 'Effects', sequence: 'Sequence', patterns: 'Patterns', choose: 'Effects',
      ledColors: 'LED colors', ledOne: 'LED 1 · blink(1)', ledTwo: 'LED 2 · ThingM', separateSolid: 'Set a steady color for each LED in one operation.', color: 'Color', target: 'Target color', flash: 'Flash color', peak: 'Peak color', a: 'Color A', b: 'Color B',
      apply: 'Apply to', assignment: 'Color assignment', after: 'After playback', off: 'Turn off', hold: 'Keep final state',
      stopDevice: 'Stop device playback', playDevice: 'Play', playLabel: 'Play on device: validate and play the current draft.', preview: 'Preview', previewLabel: 'Screen preview: play on screen without controlling the light.', executionNote: 'Preview stays on screen.', format: 'format', picker: 'picker',
      colorHelp: 'Use a color name or hex code.', textFormat: 'Text', textFormatHelp: 'Color name, hex code or @saved color', custom: 'Custom JSON', customHelp: 'Edit the request below. Playback uses this JSON.',
      advanced: 'Advanced tools',
      actions: { set: 'Solid', off: 'Off', fade: 'Fade', blink: 'Blink', breathe: 'Breathe', pulse: 'Pulse', alternate: 'Alternate', orbit: 'Orbit', rainbow: 'Rainbow' },
      descriptions: {
        set: 'Keep the selected LEDs at one color.', off: 'Turn the selected LEDs off.',
        fade: 'Fade from the light’s current color to this target when playback starts.',
        blink: 'Alternate this color with off.', breathe: 'Breathe from off up to this color, then back.',
        pulse: 'Rise quickly to this color, then fade out.', rainbow: 'Cycle through seven colors on the selected LEDs.',
      },
      pair: (first, second, orbit) => `Start A → LED ${first} · B → LED ${second}; then ${orbit ? 'crossfade' : 'swap'} the two colors.`,
    },
    'zh-CN': {
      controls: '灯光设置', effects: '效果', sequence: '序列', patterns: '图案', choose: '效果',
      ledColors: '灯色', ledOne: 'LED 1 · blink(1)', ledTwo: 'LED 2 · ThingM', separateSolid: '一次操作，分别设置两路灯的常亮颜色。', color: '颜色', target: '目标颜色', flash: '亮灯颜色', peak: '最亮时的颜色', a: '颜色 A', b: '颜色 B',
      apply: '作用于', assignment: '颜色分配', after: '播放结束后', off: '熄灯', hold: '保留最后状态',
      stopDevice: '停止设备播放', playDevice: '播放', playLabel: '设备播放：校验并播放当前草稿。', preview: '预览', previewLabel: '屏幕预览：仅在屏幕播放，不控制实体灯。', executionNote: '预览仅在屏幕播放。', format: '格式', picker: '取色器',
      colorHelp: '可输入颜色名称或十六进制色值。', textFormat: '文本', textFormatHelp: '颜色名称、十六进制或 @已存颜色', custom: '自定义 JSON', customHelp: '在下方编辑请求，播放时使用这份 JSON。',
      advanced: '高级工具',
      actions: { set: '常亮', off: '熄灭', fade: '渐变', blink: '闪烁', breathe: '呼吸', pulse: '脉冲', alternate: '交替', orbit: '环绕', rainbow: '彩虹' },
      descriptions: {
        set: '让选中的灯保持一种颜色。', off: '关闭选中的灯。',
        fade: '播放时，从灯的当前颜色渐变到此目标色。',
        blink: '在此颜色与熄灭之间切换。', breathe: '从熄灭逐渐亮起，再缓缓熄灭。',
        pulse: '快速亮起，再逐渐熄灭。', rainbow: '在选中的灯上依次播放七种颜色。',
      },
      pair: (first, second, orbit) => `开始：A → ${first} 号灯，B → ${second} 号灯；随后两色${orbit ? '交叉渐变' : '互换'}。`,
    },
  };
  const $ = id => document.getElementById(id);
  const attr = (node, name, value) => { if (node && node.getAttribute(name) !== value) node.setAttribute(name, value); };
  function text(node, value) {
    if (!node || value === undefined) return;
    attr(node, 'data-i18n-skip', '');
    node.removeAttribute('data-i18n');
    if (node.textContent !== value) node.textContent = value;
  }
  function labelFor(control) {
    if (!control) return null;
    if (control.matches('fieldset')) return control.querySelector(':scope > legend');
    return document.querySelector(`label[for="${control.id}"]`) || control.closest('label')?.querySelector(':scope > span:not(.select-picker)');
  }
  function namedInput(control, label, help = false) {
    if (!control) return;
    attr(control, 'aria-label', label);
    if (help) attr(control, 'aria-describedby', 'action-description');
  }
  function actionCaption(button, value) {
    let label = button.querySelector('strong, [data-composer-action-label]');
    if (!label) {
      label = document.createElement('span'); label.dataset.composerActionLabel = '';
      const old = [...button.childNodes].find(node => node.nodeType === Node.TEXT_NODE && node.textContent.trim());
      if (old) old.replaceWith(label); else button.append(label);
    }
    text(label, value); attr(button, 'aria-label', value);
  }
  let queued = false;
  function paint() {
    queued = false;
    const locale = document.documentElement.lang === 'zh-CN' ? 'zh-CN' : 'en', words = copy[locale];
    const buttons = browser ? document.querySelectorAll('#presets [data-kind]') : document.querySelectorAll('.effect-bank [data-action]');
    let kind = composer.dataset.activeAction || 'set';
    for (const button of buttons) {
      const action = browser ? button.dataset.kind : button.dataset.action;
      actionCaption(button, words.actions[action]);
      if (button.getAttribute('aria-pressed') === 'true') kind = action;
    }
    if (composer.dataset.activeAction !== kind) composer.dataset.activeAction = kind;
    const actionMode = browser ? !$('action-fields')?.hidden : !$('action-pane')?.hidden;
    const separate = kind === 'set' && (browser ? document.querySelector('#param-led input:checked')?.value : $('param-led')?.value) === 'separate';
    const pair = kind === 'alternate' || kind === 'orbit';
    const noColor = kind === 'off' || kind === 'rainbow';
    const count = actionMode ? noColor ? 0 : pair || separate ? 2 : 1 : 0;
    if (composer.dataset.colorCount !== String(count)) composer.dataset.colorCount = String(count);
    colorSetsPaint(locale, count);
    text(composer.querySelector(':scope > .section-title h2'), words.controls);
    text($('create-title') || document.querySelector('.effect-bank-heading'), words.choose);
    for (const [id, value] of [['mode-action', words.effects], ['mode-sequence', words.sequence], ['mode-pattern', words.patterns]]) text($(id), value);
    const title = browser ? $('action-title') : document.querySelector('[data-composer-action-title]');
    text(title, actionMode ? words.actions[kind] : browser ? words.custom : words.actions[kind]);
    const order = browser ? document.querySelector('#param-leds input:checked')?.value : $('param-order')?.value;
    const [first, second] = order === '2,1' ? [2, 1] : [1, 2];
    if ($('action-description')) $('action-description').hidden = actionMode && ['set', 'off'].includes(kind);
    text($('action-description'), !actionMode && browser ? words.customHelp : separate ? words.separateSolid : pair ? words.pair(first, second, kind === 'orbit') : words.descriptions[kind]);
    const primary = separate ? words.ledOne : pair ? words.a : kind === 'fade' ? words.target : kind === 'blink' ? words.flash : ['breathe', 'pulse'].includes(kind) ? words.peak : words.color;
    if (browser) {
      for (const [index, label] of [primary, separate ? words.ledTwo : words.b].entries()) {
        const input = $('color-input-' + index), picker = $('color-picker-' + index);
        text(labelFor(input), label); namedInput(input, label, true); namedInput(picker, `${label} ${words.picker}`, true);
      }
      text(document.querySelector('.color-help'), words.colorHelp); if (document.querySelector('.color-help')) document.querySelector('.color-help').hidden = true;
      text(labelFor($('param-led')), kind === 'set' ? words.ledColors : words.apply); text(labelFor($('param-leds')), words.assignment); text(labelFor($('param-on_finish')), words.after);
      for (const option of document.querySelectorAll('#param-leds .choice')) {
        const value = option.querySelector('input')?.value;
        text(option.querySelector('span'), value === '2,1' ? 'A → LED 2 · B → LED 1' : 'A → LED 1 · B → LED 2');
      }
    } else {
      for (const [suffix, label] of [['a', primary], ['b', separate ? words.ledTwo : words.b]]) {
        text($('color-card-' + suffix)?.querySelector('legend'), label);
        text(document.querySelector(`label[for="color-${suffix}"]`), label);
        namedInput($('color-' + suffix), label, true); namedInput($('picker-' + suffix), `${label} ${words.picker}`, true);
        namedInput($('color-mode-' + suffix), `${label} ${words.format}`);
        const format = $('color-mode-' + suffix);
        text(format?.querySelector('option[value="text"]'), words.textFormat);
        attr($('color-mode-' + suffix + '-trigger'), 'title', format?.value === 'text' ? words.textFormatHelp : `${label} ${words.format}`);
      }
      text(labelFor($('param-led')), kind === 'set' ? words.ledColors : words.apply); text(labelFor($('param-order')), words.assignment); text(labelFor($('param-finish')), words.after);
      for (const option of $('param-order')?.options || []) text(option, option.value === '2,1' ? 'A → LED 2 · B → LED 1' : 'A → LED 1 · B → LED 2');
    }
    text($('play'), words.playDevice); namedInput($('play'), words.playLabel);
    if (browser) namedInput($('stop'), words.stopDevice);
    text($('preview'), words.preview); namedInput($('preview'), words.previewLabel); $('preview')?.removeAttribute('title');
    if (!browser) text($('execution-note'), words.executionNote);
    for (const node of document.querySelectorAll('[data-composer-copy="advanced"]')) text(node, words.advanced);
  }
  function schedule() { if (!queued) { queued = true; requestAnimationFrame(paint); } }
  new MutationObserver(schedule).observe(composer, { childList: true, subtree: true, attributes: true, attributeFilter: ['hidden', 'aria-selected', 'aria-pressed'] });
  new MutationObserver(schedule).observe(document.documentElement, { attributes: true, attributeFilter: ['lang'] });
  document.addEventListener('input', event => { if (event.target.closest('.composer')) schedule(); }, true);
  document.addEventListener('change', event => { if (event.target.closest('.composer')) schedule(); }, true);
  paint();
  document.dispatchEvent(new CustomEvent('nb1-color-sets-ready'));
})();
