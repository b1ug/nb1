/* Human-facing names only. The original form values, compiler and playback own behavior. */
(() => {
  'use strict';
  const browser = document.documentElement.dataset.nb1App === 'browser';
  const composer = document.querySelector('.composer');
  if (!composer) return;
  const copy = {
    en: {
      controls: 'Light controls', effects: 'Effects', sequence: 'Sequence', patterns: 'Saved patterns', choose: 'Choose effect',
      ledColors: 'LED colors', ledOne: 'LED 1 · blink(1)', ledTwo: 'LED 2 · ThingM', separateSolid: 'Set a steady color for each LED in one operation.', color: 'Color', target: 'Target color', flash: 'Flash color', peak: 'Peak color', a: 'Color A', b: 'Color B',
      apply: 'Apply to', assignment: 'Color assignment', after: 'After playback', off: 'Turn off', hold: 'Keep final state',
      playDevice: 'Play on device', preview: 'Screen preview', previewLabel: 'Screen preview: play on screen without controlling the light.', executionNote: 'Screen preview plays here. Play on device controls the light. Saving does not play.', format: 'format', picker: 'picker',
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
      controls: '灯光设置', effects: '效果', sequence: '序列', patterns: '已存图案', choose: '选择效果',
      ledColors: '灯光颜色', ledOne: 'LED 1 · blink(1)', ledTwo: 'LED 2 · ThingM', separateSolid: '一次操作，分别设置两路灯的常亮颜色。', color: '颜色', target: '目标颜色', flash: '亮灯颜色', peak: '最亮时的颜色', a: '颜色 A', b: '颜色 B',
      apply: '作用于', assignment: '颜色分配', after: '播放结束后', off: '熄灯', hold: '保留最后状态',
      playDevice: '设备播放', preview: '屏幕预览', previewLabel: '屏幕预览：仅在屏幕播放，不控制实体灯。', executionNote: '屏幕预览在这里播放；设备播放会控制实体灯。保存不会播放。', format: '格式', picker: '取色器',
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
    text(composer.querySelector(':scope > .section-title h2'), words.controls);
    text($('create-title') || document.querySelector('.effect-bank-heading'), words.choose);
    for (const [id, value] of [['mode-action', words.effects], ['mode-sequence', words.sequence], ['mode-pattern', words.patterns]]) text($(id), value);
    const title = browser ? $('action-title') : document.querySelector('[data-composer-action-title]');
    text(title, actionMode ? words.actions[kind] : browser ? words.custom : words.actions[kind]);
    const order = browser ? document.querySelector('#param-leds input:checked')?.value : $('param-order')?.value;
    const [first, second] = order === '2,1' ? [2, 1] : [1, 2];
    text($('action-description'), !actionMode && browser ? words.customHelp : separate ? words.separateSolid : pair ? words.pair(first, second, kind === 'orbit') : words.descriptions[kind]);
    const primary = separate ? words.ledOne : pair ? words.a : kind === 'fade' ? words.target : kind === 'blink' ? words.flash : ['breathe', 'pulse'].includes(kind) ? words.peak : words.color;
    if (browser) {
      for (const [index, label] of [primary, separate ? words.ledTwo : words.b].entries()) {
        const input = $('color-input-' + index), picker = $('color-picker-' + index);
        text(labelFor(input), label); namedInput(input, label, true); namedInput(picker, `${label} ${words.picker}`, true);
      }
      text(document.querySelector('.color-help'), words.colorHelp);
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
    text($('play'), words.playDevice);
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
})();
