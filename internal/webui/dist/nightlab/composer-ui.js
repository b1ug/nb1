/* Human-facing names only. The original form values, compiler and playback own behavior. */
(() => {
  'use strict';
  const browser = document.documentElement.dataset.nb1App === 'browser';
  const composer = document.querySelector('.composer');
  if (!composer) return;
  // UI-local lamp recipes; no compiler, saved Pattern or execution behavior.
  const essentials = {"id":"essentials","label":{"en":"Common colors","zh-CN":"常用颜色"},"character":{"en":"Common colors","zh-CN":"常用颜色"},"colors":["#ff0000","#ffa500","#ffff00","#00ff00","#00a060","#00ffff","#0000ff","#8f00ff","#ffffff"],"pair":["#ff0000","#0000ff"]};
  const sets = [{"id":"candlelight","label":{"en":"Afterglow","zh-CN":"晚霞余晖"},"character":{"en":"Warm sunset, leaves and violet dusk","zh-CN":"暖色夕照、叶影与紫色暮光"},"colors":["#c00000","#c04800","#b87408","#c0a008","#387c18","#2438a0","#644c98","#ac286c"],"pair":["#c04800","#2438a0"]},{"id":"tide","label":{"en":"Sea Breeze","zh-CN":"海风轻拂"},"character":{"en":"Sea water, warm sand and shore accents","zh-CN":"海水、暖沙与岸边点色"},"colors":["#c00000","#b87408","#80b000","#00a000","#006868","#2438a0","#6000c0","#ac286c"],"pair":["#006868","#b87408"]},{"id":"petals","label":{"en":"Mist Garden","zh-CN":"雾色花园"},"character":{"en":"Morandi-inspired clay, leaves and quiet flowers","zh-CN":"莫兰迪风格的雾陶、叶色与克制花色"},"colors":["#8c3c40","#985c48","#8c783c","#527458","#3c7474","#485c8c","#684c80","#80506c"],"pair":["#80506c","#527458"]},{"id":"neon","label":{"en":"City Lights","zh-CN":"霓虹夜色"},"character":{"en":"Eight bright city lights","zh-CN":"八种鲜明的城市灯色"},"colors":["#c00000","#c04800","#80b000","#00a000","#00a0b0","#2050bc","#6000c0","#c00068"],"pair":["#c00068","#00a0b0"]},{"id":"forest","label":{"en":"Woodland Glow","zh-CN":"林间微光"},"character":{"en":"Earth, leaves, water and autumn fruit","zh-CN":"泥土、叶片、溪流与秋日果实"},"colors":["#c00000","#a44418","#b87408","#c0a008","#387c18","#006868","#2438a0","#88385c"],"pair":["#387c18","#b87408"]},{"id":"dusk","label":{"en":"Midnight Drift","zh-CN":"午夜漫游"},"character":{"en":"Inky streets and distant light","zh-CN":"墨色街景与远处光点"},"colors":["#600818","#b87408","#80b000","#144830","#004c58","#141c70","#482068","#88385c"],"pair":["#141c70","#b87408"]},{"id":"citrus","label":{"en":"Summer Orchard","zh-CN":"夏日果园"},"character":{"en":"Fruit reds, warm skins and fresh greens","zh-CN":"果实红、暖色果皮与鲜绿"},"colors":["#600818","#c00000","#985c48","#c04800","#c0a008","#80b000","#00a000","#ac286c"],"pair":["#c04800","#00a000"]},{"id":"aurora","label":{"en":"Aurora","zh-CN":"极光流转"},"character":{"en":"Layered greens, blues and upper red glow","zh-CN":"层叠绿蓝与上缘红光"},"colors":["#c00000","#387c18","#00a000","#00a0b0","#2050bc","#644c98","#6000c0","#c00068"],"pair":["#00a000","#c00068"]}];
  const hueColumns = ['red', 'orange', 'gold', 'green', 'cyan', 'blue', 'violet', 'rose'];
  const hueNames = {en: ['Red', 'Orange', 'Yellow', 'Green', 'Cyan', 'Blue', 'Violet', 'Rose'], 'zh-CN': ['红', '橙', '黄', '绿', '青', '蓝', '紫', '玫红']};
  // Editorial categories and names, not color conversion or measured appearance.
  const colorBank = {"#600818":["red","Ember","余烬红"],"#8c3c40":["red","Dusty brick","灰砖红"],"#c00000":["red","Red","正红"],"#985c48":["orange","Mist clay","雾陶色"],"#a44418":["orange","Clay","陶土橙"],"#c04800":["orange","Orange","鲜橙"],"#b87408":["orange","Amber","琥珀"],"#8c783c":["gold","Soft ochre","柔赭色"],"#c0a008":["gold","Gold","金黄"],"#80b000":["gold","Lime","青柠"],"#387c18":["green","Leaf","叶绿"],"#00a000":["green","Green","翠绿"],"#527458":["green","Sage","鼠尾草绿"],"#144830":["green","Night pine","夜杉绿"],"#006868":["cyan","Teal","溪水青"],"#3c7474":["cyan","Dusty teal","雾青"],"#00a0b0":["cyan","Cyan","亮青"],"#004c58":["cyan","Night teal","夜青"],"#2050bc":["blue","Blue","湛蓝"],"#485c8c":["blue","Slate blue","灰蓝"],"#2438a0":["blue","Indigo","靛蓝"],"#141c70":["blue","Ink blue","墨蓝"],"#644c98":["violet","Orchid","兰紫"],"#6000c0":["violet","Violet","电紫"],"#684c80":["violet","Heather","石楠紫"],"#482068":["violet","Night violet","夜紫"],"#80506c":["rose","Dusty rose","灰玫红"],"#c00068":["rose","Pink","桃红"],"#ac286c":["rose","Rose","玫红"],"#88385c":["rose","Berry","莓红"]};
  for (const set of [essentials, ...sets]) { Object.freeze(set.label); Object.freeze(set.character); Object.freeze(set.colors); Object.freeze(set.pair); Object.freeze(set); }
  Object.freeze(sets);
  let selectedId = 'essentials';
  const getSet = id => id === 'essentials' ? essentials : sets.find(set => set.id === id);
  const chooser = document.createElement('details'); chooser.id = 'color-sets'; chooser.className = 'color-sets'; chooser.setAttribute('data-i18n-skip', '');
  const summary = document.createElement('summary'), heading = document.createElement('span'), selectedName = document.createElement('span'); selectedName.id = 'color-set-name'; summary.append(heading, selectedName);
  function browse(id) { selectedId = id; paint(); document.dispatchEvent(new CustomEvent('nb1-color-set-browse', {detail: {id}})); }
  const commonActions = document.createElement('div'); commonActions.className = 'color-set-actions';
  const commonButton = document.createElement('button'); commonButton.id = 'color-set-common'; commonButton.type = 'button'; commonButton.className = 'secondary'; commonButton.addEventListener('click', () => browse('essentials')); commonActions.append(commonButton);
  const grid = document.createElement('div'); grid.className = 'color-set-grid';
  const cards = sets.map(set => {
    const button = document.createElement('button'); button.type = 'button'; button.className = 'color-set-card'; button.dataset.colorSet = set.id;
    const label = document.createElement('span'), strip = document.createElement('span'); label.className = 'color-set-label'; strip.className = 'color-set-strip'; strip.setAttribute('aria-hidden', 'true');
    for (const color of set.colors) { const swatch = document.createElement('span'); swatch.style.backgroundColor = color; strip.append(swatch); }
    button.append(label, strip); button.addEventListener('click', () => browse(set.id)); grid.append(button);
    return {set, button, label};
  });
  const setActions = document.createElement('div'); setActions.className = 'color-set-actions';
  const pairButton = document.createElement('button'), sequenceButton = document.createElement('button'); pairButton.id = 'color-set-pair'; sequenceButton.id = 'color-set-sequence';
  for (const [button, event] of [[pairButton, 'nb1-color-set-pair'], [sequenceButton, 'nb1-color-set-sequence']]) { button.type = 'button'; button.className = 'secondary'; button.addEventListener('click', () => document.dispatchEvent(new CustomEvent(event, {detail: {id: selectedId}}))); }
  const comparison = document.createElement('details'); comparison.id = 'color-set-compare';
  const compareSummary = document.createElement('summary'), compareNote = document.createElement('p'); compareNote.id = 'color-set-compare-note';
  const compareScroll = document.createElement('div'); compareScroll.id = 'color-set-compare-scroll'; compareScroll.tabIndex = 0; compareScroll.setAttribute('role', 'region'); compareScroll.setAttribute('aria-describedby', compareNote.id);
  const table = document.createElement('table'); table.id = 'color-set-compare-table';
  const tableHead = table.createTHead().insertRow(), groupHead = document.createElement('th'); groupHead.scope = 'col'; tableHead.append(groupHead);
  const columnHeads = hueColumns.map(() => { const th = document.createElement('th'); th.scope = 'col'; tableHead.append(th); return th; });
  const body = table.createTBody(), compareColors = [];
  const compareRows = sets.map(set => {
    const row = body.insertRow(); row.dataset.colorSetRow = set.id;
    const label = document.createElement('th'); label.scope = 'row'; row.append(label);
    const cells = hueColumns.map(() => row.insertCell());
    for (const color of set.colors) {
      const info = colorBank[color], chip = document.createElement('span'), swatch = document.createElement('span'), code = document.createElement('code');
      chip.className = 'color-set-compare-chip'; chip.dataset.colorSetCompareHex = color; chip.setAttribute('role', 'img');
      swatch.style.backgroundColor = color; swatch.setAttribute('aria-hidden', 'true'); code.textContent = color; chip.append(swatch, code);
      cells[hueColumns.indexOf(info[0])].append(chip); compareColors.push({set, color, info, chip});
    }
    return {set, label};
  });
  compareScroll.append(table); comparison.append(compareSummary, compareNote, compareScroll);
  setActions.append(pairButton, sequenceButton); chooser.append(summary, commonActions, grid, setActions, comparison);
  window.NB1ColorSets = Object.freeze({sets, get: getSet, get selectedId() { return selectedId; }, refresh: schedule,
    sequence(id) { const set = sets.find(set => set.id === id); if (!set) throw Error('Choose a theme to create a Sequence'); return {timing: 'serial', steps: set.colors.map(color => ({targets: [{led: 0, color}], fade_ms: 600, hold_ms: 400})), repeat: 3, on_finish: 'off'}; }
  });
  function colorSetsPaint(locale, count) {
    const target = browser ? document.querySelector('#action-fields > .color-field') : composer.querySelector('.color-pair');
    const host = browser ? document.getElementById('action-fields') : target?.parentElement;
    if (host && (chooser.parentElement !== host || target && chooser.nextSibling !== target)) host.insertBefore(chooser, target || null);
    const hidden = count === 0; if (chooser.hidden !== hidden) chooser.hidden = hidden;
    if (pairButton.hidden !== (count !== 2)) pairButton.hidden = count !== 2;
    const common = selectedId === 'essentials'; if (sequenceButton.hidden !== common) sequenceButton.hidden = common;
    if (setActions.hidden !== (common && count !== 2)) setActions.hidden = common && count !== 2;
    text(commonButton, locale === 'zh-CN' ? '常用颜色 · 9' : 'Common colors · 9'); attr(commonButton, 'aria-pressed', String(common));
    text(heading, locale === 'zh-CN' ? '配色组' : 'Color sets'); text(selectedName, getSet(selectedId).label[locale]);
    text(pairButton, locale === 'zh-CN' ? '应用双色' : 'Use pair'); text(sequenceButton, locale === 'zh-CN' ? '创建序列' : 'Create Sequence');
    attr(pairButton, 'title', `A: ${getSet(selectedId).pair[0]} · B: ${getSet(selectedId).pair[1]}`);
    const compareLabel = locale === 'zh-CN' ? '按色系对比' : 'Compare by hue';
    text(compareSummary, compareLabel); attr(compareScroll, 'aria-label', compareLabel); attr(table, 'aria-label', compareLabel);
    text(compareNote, locale === 'zh-CN' ? '相同色值为同一配方' : 'Same hex = same recipe');
    text(groupHead, locale === 'zh-CN' ? '配色组' : 'Color set');
    columnHeads.forEach((node, index) => text(node, hueNames[locale][index]));
    for (const {set, label} of compareRows) text(label, set.label[locale]);
    for (const {set, color, info, chip} of compareColors) { const title = `${set.label[locale]} · ${info[locale === 'zh-CN' ? 2 : 1]} · ${color}`; attr(chip, 'title', title); attr(chip, 'aria-label', title); }
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
