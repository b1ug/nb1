/* Presentation only: Go-resolved drafts, cached readback and a separate screen preview. */
(() => {
  'use strict';
  const stage = document.querySelector('[data-light-stage]');
  if (!stage) return;
  const browser = document.documentElement.dataset.nb1App === 'browser';
  stage.dataset.studioSource = 'none';
  // Face wordmarks are 2D projections of the original diffuser CAD engravings,
  // not redrawn brand art. Derived from todbot/blink1 fixed commit
  // 101abe3599c661c4dd4d1204b4d50fb592293143, CC BY-SA 4.0;
  // see model-assets/LICENSE.txt and model-assets/source.json.
  const faceWordmarks = ["M96.235 67.842 L94.000 67.831 L94.000 84.057 L99.536 84.082 L103.233 84.097 L103.233 81.863 L99.536 81.848 L96.235 81.834 L96.235 74.740 L99.536 74.754 L103.233 74.769 L103.233 72.535 L99.536 72.520 L96.235 72.506 Z M122.895 67.926 L122.289 67.925 L122.289 70.159 L122.895 70.160 L124.523 70.163 L124.523 67.929 Z M140.951 74.939 L139.393 74.939 L139.393 84.169 L140.951 84.169 Z M153.211 79.595 L150.977 79.598 L150.977 81.832 L153.211 81.829 Z M155.544 72.596 L153.309 72.599 L153.309 74.833 L155.544 74.830 Z M161.003 72.587 L158.769 72.591 L158.769 79.489 L161.003 79.484 Z M165.669 67.912 L163.434 67.917 L163.434 70.151 L165.669 70.146 Z M165.669 81.904 L163.434 81.909 L163.434 84.143 L165.669 84.138 Z M172.267 72.460 L172.267 81.886 L169.934 81.893 L169.934 84.126 L176.834 84.104 L176.834 81.870 L174.502 81.878 L174.502 67.886 L172.267 67.893 L172.267 70.226 L169.934 70.233 L169.934 72.467 Z M185.667 70.174 L183.433 70.184 L183.433 72.418 L185.667 72.408 Z M185.667 79.502 L183.433 79.512 L183.433 81.746 L185.667 81.736 Z M181.391 81.852 L181.100 81.854 L181.100 84.088 L181.391 84.086 L183.335 84.078 L183.335 81.844 Z M188.000 72.495 L185.765 72.506 L185.765 79.403 L188.000 79.393 Z M181.391 67.860 L181.100 67.862 L181.100 70.096 L181.391 70.095 L183.335 70.086 L183.335 67.852 Z M163.336 79.577 L161.101 79.582 L161.101 81.817 L163.336 81.812 Z M163.336 70.250 L161.101 70.254 L161.101 72.489 L163.336 72.484 Z M155.544 81.924 L153.309 81.927 L153.309 84.161 L155.544 84.158 Z M153.211 74.931 L150.977 74.934 L150.977 77.168 L153.211 77.165 Z M146.213 67.941 L143.978 67.942 L143.978 84.168 L146.213 84.167 L146.213 79.503 L146.228 79.503 L150.879 79.500 L150.879 77.266 L146.228 77.269 L146.213 77.269 Z M134.559 72.605 L130.062 72.601 L130.062 84.163 L132.297 84.165 L132.297 74.837 L134.559 74.839 L139.295 74.841 L139.295 72.607 Z M122.289 74.823 L122.289 81.917 L119.956 81.913 L119.956 84.146 L122.895 84.152 L126.856 84.159 L126.856 81.925 L124.523 81.921 L124.523 72.593 L122.895 72.590 L119.956 72.585 L119.956 74.819 Z M111.123 70.132 L111.123 81.890 L108.790 81.883 L108.790 84.116 L111.225 84.124 L115.690 84.136 L115.690 81.903 L113.358 81.896 L113.358 67.904 L111.225 67.898 L108.790 67.890 L108.790 70.124 Z M105.566 74.875 L103.331 74.867 L103.331 81.765 L105.566 81.774 Z","M155.886 66.158 L155.983 66.060 L155.983 66.912 L156.136 66.912 L157.489 66.909 L157.489 62.727 L156.136 62.730 L154.377 62.733 L154.377 64.318 L155.527 64.316 L155.393 64.594 L155.359 64.652 L155.343 64.674 L155.226 64.841 L155.225 64.843 L155.151 64.919 L155.134 64.932 L155.072 64.981 L154.915 65.071 L154.847 65.099 L154.684 65.165 L154.609 65.182 L154.408 65.228 L154.339 65.233 L154.126 65.249 L153.790 65.231 L153.781 65.230 L153.569 65.190 L153.549 65.183 L153.345 65.112 L153.337 65.109 L153.301 65.092 L153.165 65.012 L153.123 64.986 L152.988 64.875 L152.956 64.848 L152.774 64.655 L152.730 64.594 L152.620 64.445 L152.587 64.379 L152.503 64.213 L152.479 64.148 L152.473 64.131 L152.404 63.854 L152.357 63.342 L152.356 63.335 L152.367 62.750 L152.369 62.674 L152.383 62.513 L152.417 62.353 L152.458 62.158 L152.480 62.088 L152.561 61.834 L152.597 61.751 L152.640 61.652 L152.662 61.600 L152.689 61.560 L152.715 61.521 L152.918 61.303 L152.941 61.285 L153.133 61.128 L153.145 61.119 L153.255 61.055 L153.282 61.039 L153.340 61.017 L153.365 61.008 L153.591 60.945 L153.747 60.918 L153.875 60.896 L153.919 60.896 L154.056 60.897 L154.077 60.900 L154.201 60.921 L154.404 60.981 L154.440 60.997 L154.542 61.041 L154.575 61.062 L154.671 61.124 L154.702 61.152 L154.830 61.270 L154.892 61.339 L154.949 61.413 L154.976 61.449 L155.011 61.516 L155.032 61.556 L155.085 61.682 L155.122 61.769 L155.131 61.796 L155.169 61.903 L156.136 61.901 L157.375 61.898 L157.357 61.642 L157.350 61.594 L157.340 61.552 L157.269 61.254 L157.249 61.206 L157.127 60.904 L157.092 60.842 L156.977 60.636 L156.836 60.446 L156.650 60.192 L156.352 59.885 L156.192 59.719 L156.136 59.683 L155.719 59.419 L155.494 59.310 L155.223 59.179 L155.051 59.118 L155.036 59.112 L154.696 59.034 L154.661 59.026 L154.337 58.995 L153.898 58.997 L153.504 58.999 L153.020 59.053 L152.971 59.058 L152.743 59.113 L152.708 59.121 L152.207 59.340 L151.972 59.469 L151.732 59.600 L151.572 59.702 L151.444 59.801 L151.151 60.030 L151.021 60.162 L150.819 60.418 L150.715 60.552 L150.491 60.916 L150.479 60.943 L150.315 61.307 L150.222 61.707 L150.180 61.968 L150.107 62.408 L150.033 63.140 L150.034 63.266 L150.036 63.493 L150.111 64.006 L150.230 64.474 L150.309 64.788 L150.430 65.072 L150.553 65.361 L150.606 65.457 L150.663 65.540 L150.877 65.853 L150.928 65.906 L151.197 66.187 L151.286 66.263 L151.753 66.595 L151.837 66.654 L152.308 66.885 L152.362 66.911 L152.815 67.047 L152.887 67.068 L153.171 67.114 L153.295 67.119 L153.705 67.135 L153.836 67.127 L154.211 67.067 L154.317 67.050 L154.396 67.030 L154.674 66.931 L154.882 66.858 L155.128 66.722 L155.353 66.599 L155.697 66.345 L155.714 66.333 L155.804 66.251 L155.812 66.242 L155.877 66.167 Z M132.291 59.220 L129.996 59.217 L129.996 66.997 L132.314 67.000 L132.314 63.848 L134.668 63.851 L134.668 66.956 L136.650 66.957 L137.009 66.957 L137.009 59.223 L136.650 59.223 L134.644 59.222 L134.644 61.884 L132.291 61.881 Z M138.265 59.224 L138.334 66.981 L140.606 66.982 L140.606 59.224 Z M120.062 55.056 L120.000 57.693 L126.889 57.708 L136.650 57.719 L146.395 57.719 L156.136 57.706 L159.487 57.699 L159.487 94.411 L156.136 94.418 L146.395 94.431 L136.650 94.432 L126.889 94.420 L122.575 94.411 L122.575 62.534 L120.000 62.528 L120.000 96.917 L126.889 96.932 L136.650 96.944 L146.395 96.943 L156.136 96.930 L162.000 96.917 L162.000 55.056 L156.136 55.070 L146.395 55.082 L136.650 55.083 L126.889 55.072 Z M136.650 89.442 L137.714 92.741 L144.224 92.741 L146.395 85.906 L149.273 76.844 L149.304 92.676 L156.136 92.666 L157.516 92.663 L157.516 68.329 L156.136 68.331 L146.395 68.344 L145.064 68.345 L140.938 82.844 L136.781 68.376 L136.650 68.376 L126.889 68.364 L124.453 68.360 L124.453 92.663 L126.889 92.668 L132.665 92.676 L132.665 77.094 Z M144.375 59.247 L141.955 59.248 L141.955 66.935 L144.179 66.934 L144.179 62.605 L146.395 66.485 L146.664 66.956 L148.944 66.954 L148.944 59.220 L146.696 59.222 L146.696 63.413 L146.395 62.873 Z M120.000 59.173 L120.023 61.070 L124.508 61.081 L124.508 66.918 L126.872 66.922 L126.872 61.132 L126.889 61.132 L129.091 61.136 L129.091 59.192 L126.889 59.188 Z"];
  stage.innerHTML = `
    <div class="stage-theatre">
      <div class="stage-fallback" data-stage-fallback role="group" aria-label="Two-dimensional views of the two fixed LED faces">
        ${['one', 'two'].map((key, index) => `<figure class="stage-face" data-fallback-face="${key}" data-led="${index + 1}" data-face-state="unspecified" role="img">
          <div class="stage-face-heading"><strong>LED ${index + 1}</strong><span data-fallback-side="${key}"></span></div>
          <svg class="stage-face-art" viewBox="0 0 240 152" aria-hidden="true" focusable="false">
            <defs><pattern id="face-unspecified-${key}" width="9" height="9" patternUnits="userSpaceOnUse" patternTransform="rotate(35)"><path d="M0 0V9" class="stage-face-hatch-line"/></pattern></defs>
            <path class="stage-face-usb" d="M14 49H72V103H14Z"/>
            ${index === 0 ? '<path class="stage-face-usb-hole" d="M31 57H42V64H31ZM50 57H61V64H50Z"/>' : '<path class="stage-face-usb-seam" d="M19 54H65V98H19Z"/>'}
            <path class="stage-face-frame" d="M68 24H184C202 24 216 38 216 56V96C216 114 202 128 184 128H68Z"/>
            <path class="stage-face-diffuser" d="M73 28H183C199 28 212 40 212 56V96C212 112 199 124 183 124H73Z"/>
            <path class="stage-face-unspecified" fill="url(#face-unspecified-${key})" d="M73 28H183C199 28 212 40 212 56V96C212 112 199 124 183 124H73Z"/>
            <path class="stage-face-wordmark" fill-rule="evenodd" d="${faceWordmarks[index]}"/>
          </svg>
          <figcaption><span class="stage-face-state" data-fallback-state></span><code data-fallback-value>—</code></figcaption>
        </figure>`).join('')}
      </div><span class="stage-dimension" data-type-role="caption">blink(1) mk2</span>
    </div>
    <div class="stage-channels">
      <div class="stage-channel"><span id="studio-led-one">LED 1 · blink(1)</span><output data-studio-color="one" aria-labelledby="studio-led-one" aria-live="off">—</output></div>
      <div class="stage-channel"><span id="studio-led-two">LED 2 · ThingM</span><output data-studio-color="two" aria-labelledby="studio-led-two" aria-live="off">—</output></div>
    </div>
    <div class="stage-footer"><span data-studio-text="source"></span></div>`;
  const copy = {
    en: { draft: 'Draft colors', readback: 'Cached readback', sampleUnavailable: 'sample time unavailable', none: 'No resolved target', preview: 'Screen preview', previewPaused: 'Screen preview · paused', previewCompleted: 'Screen preview · complete', previewPreparing: 'Screen preview · preparing', previewStopped: 'Screen preview · stopped', previewError: 'Screen preview · unavailable', library: 'Saved colors', tune: 'Light controls', modelView: 'Light preview', faces: 'Two-dimensional views of the two fixed LED faces', front: 'Front · blink(1)', back: 'Back · ThingM', on: 'Target color', previewColor: 'Preview color', previewOff: 'Off', off: 'Off target', unspecified: 'Not specified' },
    'zh-CN': { draft: '当前草稿色', readback: '设备缓存读数', sampleUnavailable: '采样时间不可用', none: '尚无已解析目标色', preview: '屏幕预览', previewPaused: '屏幕预览 · 已暂停', previewCompleted: '屏幕预览 · 已完成', previewPreparing: '屏幕预览 · 准备中', previewStopped: '屏幕预览 · 已停止', previewError: '屏幕预览 · 暂不可用', library: '已存颜色', tune: '灯光设置', modelView: '灯光预览', faces: '两个固定灯面的二维预览', front: '正面 · blink(1)', back: '背面 · ThingM', on: '目标颜色', previewColor: '预览颜色', previewOff: '熄灭', off: '熄灭目标', unspecified: '未指定' },
  };
  let readback = [null, null], showReadback = false, sampledAt = null, screen = null, queued = false;
  const hex = text => /^#[a-f\d]{6}$/i.test(text?.trim() || '') ? text.trim().toLowerCase() : null;
  const canonical = id => hex(document.getElementById(id)?.textContent);
  function draft() {
    if (browser) {
      if (document.getElementById('action-fields')?.hidden) return [null, null];
      const one = canonical('color-result-0'), two = canonical('color-result-1');
      if (two) { const order = document.querySelector('#param-leds input:checked')?.value; return order === '2,1' ? [two, one] : [one, two]; }
      const led = document.querySelector('#param-led input:checked')?.value || '0';
      return led === '1' ? [one, null] : led === '2' ? [null, one] : [one, one];
    }
    if (document.getElementById('action-pane')?.hidden) return [null, null];
    const one = canonical('resolved-a');
    const secondVisible = document.getElementById('color-card-b')?.hidden === false;
    if (secondVisible) { const two = canonical('resolved-b'); return document.getElementById('param-order')?.value === '2,1' ? [two, one] : [one, two]; }
    const firstHidden = document.getElementById('color-card-a')?.hidden;
    if (firstHidden) return [null, null];
    const led = document.getElementById('param-led')?.value || '0';
    return led === '1' ? [one, null] : led === '2' ? [null, one] : [one, one];
  }
  function paint() {
    queued = false;
    const values = screen ? screen.colors : showReadback ? readback : draft();
    const source = screen ? 'preview' : showReadback ? 'readback' : values.some(Boolean) ? 'draft' : 'none';
    const sourceKey = screen ? ({ paused: 'previewPaused', completed: 'previewCompleted', preparing: 'previewPreparing', stopped: 'previewStopped', error: 'previewError' }[screen.state] || 'preview') : source;
    if (stage.dataset.studioSource !== source) stage.dataset.studioSource = source;
    const language = document.documentElement.lang === 'zh-CN' ? 'zh-CN' : 'en', words = copy[language];
    const fallback = stage.querySelector('[data-stage-fallback]');
    if (fallback.getAttribute('aria-label') !== words.faces) fallback.setAttribute('aria-label', words.faces);
    for (const node of document.querySelectorAll('[data-studio-text]')) {
      const value = words[node.dataset.studioText === 'source' ? sourceKey : node.dataset.studioText];
      if (value === undefined) continue;
      const label = node.dataset.studioText === 'source' && source === 'readback' ? `${value} · ${sampledAt || words.sampleUnavailable}` : value;
      if (node.textContent !== label) node.textContent = label;
    }
    for (const [index, key] of ['one', 'two'].entries()) {
      const value = values[index], output = stage.querySelector(`[data-studio-color="${key}"]`);
      if (output.textContent !== (value || '—')) output.textContent = value || '—';
      const color = value || 'var(--studio-sky)';
      if (stage.style.getPropertyValue(`--stage-${key}`) !== color) stage.style.setProperty(`--stage-${key}`, color);
      // A black target means off; an absent target says nothing about that face.
      // The two illustrations are fixed physical faces, never A/B draft slots.
      const face = stage.querySelector(`[data-fallback-face="${key}"]`);
      const state = !value ? 'unspecified' : value.toLowerCase() === '#000000' ? 'off' : 'on';
      if (face.dataset.faceState !== state) face.dataset.faceState = state;
      const side = index === 0 ? words.front : words.back;
      const label = face.querySelector('[data-fallback-side]'), status = face.querySelector('[data-fallback-state]'), faceValue = face.querySelector('[data-fallback-value]');
      if (label.textContent !== side) label.textContent = side;
      const stateLabel = screen && state === 'on' ? words.previewColor : screen && state === 'off' ? words.previewOff : words[state];
      if (status.textContent !== stateLabel) status.textContent = stateLabel;
      if (faceValue.textContent !== (value || '—')) faceValue.textContent = value || '—';
      const description = `LED ${index + 1} · ${side} · ${stateLabel}${value ? ` · ${value}` : ''}`;
      if (face.getAttribute('aria-label') !== description) face.setAttribute('aria-label', description);
    }
  }
  function schedule() { if (!queued) { queued = true; requestAnimationFrame(paint); } }
  const draftChanged = event => { if (event.target.closest('#action-fields, .color-card, #sequence-pane, #advanced, #device-select')) { showReadback = false; schedule(); } };
  document.addEventListener('input', draftChanged, true);
  document.addEventListener('change', draftChanged, true);
  document.addEventListener('click', event => { const effect = event.target.closest('[data-kind], [data-action]'); if (effect?.getAttribute('aria-pressed') === 'true') return; if (event.target.closest('[data-kind], [data-action], .palette, .editor-tabs')) { showReadback = false; schedule(); } }, true);
  // Screen frames have their own lifetime. They never become device acceptance
  // reports, authored inputs or the cached device observation.
  window.addEventListener('nb1-screen-preview', event => {
    const detail = event.detail;
    if (detail?.active === false) { screen = null; schedule(); return; }
    if (detail?.active !== true || !Array.isArray(detail.colors) || detail.colors.length !== 2) return;
    if (!detail.colors.every(value => value === null || typeof value === 'string' && hex(value))) return;
    screen = { colors: detail.colors.map(value => value === null ? null : hex(value)), state: detail.state };
    schedule();
  });
  window.addEventListener('nb1-cached-readback', event => {
    const detail = event.detail;
    if (!Array.isArray(detail?.colors) || detail.colors.length !== 2 || !detail.colors.every(value => value === null || hex(value))) return;
    readback = detail.colors.map(value => value === null ? null : hex(value));
    sampledAt = detail.sampledAt ? document.getElementById('sample-time')?.textContent : null;
    showReadback = true; schedule();
  });
  new MutationObserver(schedule).observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ['aria-pressed', 'hidden', 'data-state'] });
  new MutationObserver(schedule).observe(document.documentElement, { attributes: true, attributeFilter: ['lang'] });
  paint();
})();
