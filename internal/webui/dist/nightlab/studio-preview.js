import {callCompiler} from './compiler-loader.js';
import {createTimeline, sampleTimeline} from './preview-timeline.js';

// Screen playback consumes the exact compiled Sequence. It never opens a
// transport, sends a feature report, creates a Job or edits the authored draft.
const stage = document.querySelector('[data-light-stage]');
const compiledNode = document.getElementById('compiled');
if (stage && compiledNode) init();

function init() {
  const transport = document.createElement('div');
  transport.className = 'stage-preview-transport';
  transport.dataset.previewTransport = '';
  transport.dataset.i18nSkip = '';
  transport.hidden = true;
  transport.innerHTML = `<span data-preview-status role="status" aria-live="polite"></span>
    <output data-preview-time aria-live="off"></output>
    <div class="stage-preview-buttons"><button type="button" data-preview-control="pause"></button><button type="button" data-preview-control="stop"></button><button type="button" data-preview-control="replay"></button></div>
    <progress data-preview-progress value="0" max="1" aria-live="off"></progress>
    <p data-preview-assumption hidden></p>`;
  stage.append(transport);
  const $ = selector => transport.querySelector(selector);
  const buttons = Object.fromEntries(['pause', 'stop', 'replay'].map(name => [name, $(`[data-preview-control="${name}"]`)]));
  const copy = {
    en: {
      preparing: 'Preparing screen preview…', playing: 'Screen preview', paused: 'Preview paused',
      completed: 'Preview complete', stopped: 'Preview stopped', error: 'Preview unavailable. Check the request and try again.',
      pause: 'Pause', resume: 'Resume', stop: 'Stop', replay: 'Replay', seconds: 's',
      pauseLabel: 'Pause screen preview', resumeLabel: 'Resume screen preview', stopLabel: 'Stop screen preview', replayLabel: 'Replay on screen',
      progress: 'Screen preview progress', time: 'Screen preview time',
      assumption: 'An unknown starting color is shown as off; later previews use the last screen color.',
    },
    'zh-CN': {
      preparing: '正在准备屏幕预览…', playing: '屏幕预览', paused: '预览已暂停', completed: '预览已结束', stopped: '预览已停止',
      error: '暂时无法预览，请检查请求后重试。', pause: '暂停', resume: '继续', stop: '停止', replay: '重播', seconds: '秒',
      pauseLabel: '暂停屏幕预览', resumeLabel: '继续屏幕预览', stopLabel: '停止屏幕预览', replayLabel: '在屏幕中重播',
      progress: '屏幕预览进度', time: '屏幕预览时间',
      assumption: '未知的起始颜色暂按熄灭展示；后续预览从上次屏幕颜色开始。',
    },
  };
  let generation = 0, revision = 0, pending = null, run = null, frame = 0, pendingTimer = 0;
  let state = 'idle', elapsed = 0, duration = 0, assumedStart = false, errorMessage = '';
  let memory = [null, null], lastPlan = null, lastRevision = -1, published = '', lastPaint = -Infinity;
  const numeric = Object.fromEntries(['en', 'zh-CN'].map(locale => [locale, new Intl.NumberFormat(locale, {minimumFractionDigits: 1, maximumFractionDigits: 1})]));
  const text = (node, value) => { if (node.textContent !== value) node.textContent = value; };
  const attr = (node, name, value) => { if (node.getAttribute(name) !== value) node.setAttribute(name, value); };
  const property = (node, name, value) => { if (node[name] !== value) node[name] = value; };
  function words() { return copy[document.documentElement.lang === 'zh-CN' ? 'zh-CN' : 'en']; }
  function publish(colors, active) {
    const key = `${generation}:${state}:${active}:${colors.join(',')}`;
    if (key === published) return;
    published = key;
    window.dispatchEvent(new CustomEvent('nb1-screen-preview', {detail: {colors: [...colors], state, active}}));
  }
  function controls() {
    const language = document.documentElement.lang === 'zh-CN' ? 'zh-CN' : 'en', strings = words();
    stage.dataset.screenPreviewState = state;
    stage.dataset.screenPreviewRun = String(generation);
    stage.dataset.screenPreviewElapsed = String(Math.round(elapsed));
    stage.dataset.screenPreviewDuration = String(Math.round(duration));
    property(transport, 'hidden', state === 'idle');
    text($('[data-preview-status]'), strings[state] || '');
    attr($('[data-preview-status]'), 'title', errorMessage);
    const formatter = numeric[language];
    text($('[data-preview-time]'), `${formatter.format(elapsed / 1000)} / ${formatter.format(duration / 1000)} ${strings.seconds}`);
    attr($('[data-preview-time]'), 'aria-label', strings.time);
    property(buttons.pause, 'hidden', !['playing', 'paused'].includes(state));
    property(buttons.pause, 'disabled', !['playing', 'paused'].includes(state));
    const paused = state === 'paused';
    text(buttons.pause, paused ? strings.resume : strings.pause);
    attr(buttons.pause, 'aria-label', paused ? strings.resumeLabel : strings.pauseLabel);
    property(buttons.stop, 'hidden', !['playing', 'paused', 'preparing'].includes(state));
    property(buttons.stop, 'disabled', buttons.stop.hidden);
    text(buttons.stop, strings.stop); attr(buttons.stop, 'aria-label', strings.stopLabel);
    property(buttons.replay, 'hidden', !['completed', 'stopped'].includes(state) || !lastPlan || lastRevision !== revision);
    property(buttons.replay, 'disabled', buttons.replay.hidden);
    text(buttons.replay, strings.replay); attr(buttons.replay, 'aria-label', strings.replayLabel);
    const progress = $('[data-preview-progress]');
    property(progress, 'max', Math.max(1, duration)); property(progress, 'value', Math.min(elapsed, duration));
    attr(progress, 'aria-label', strings.progress);
    property($('[data-preview-assumption]'), 'hidden', !assumedStart);
    text($('[data-preview-assumption]'), strings.assumption);
  }
  function clearActivity() {
    cancelAnimationFrame(frame); frame = 0;
    clearTimeout(pendingTimer); pendingTimer = 0;
    pending = null; run = null;
  }
  function stop(next = 'stopped', invalidate = false) {
    generation++;
    clearActivity();
    state = next; errorMessage = '';
    if (invalidate) { revision++; lastPlan = null; lastRevision = -1; }
    publish(memory, false); controls();
  }
  function fail(error, id) {
    if (id !== generation) return;
    generation++;
    clearActivity(); state = 'error';
    errorMessage = String(error?.message || error || '').slice(0, 512);
    publish(memory, false); controls();
  }
  function paint(now, force = false) {
    if (!run || state !== 'playing') return;
    elapsed = Math.min(duration, Math.max(0, now - run.origin));
    if (!force && now - lastPaint < 33 && elapsed < duration) { frame = requestAnimationFrame(paint); return; }
    lastPaint = now;
    const sample = sampleTimeline(run.timeline, elapsed);
    memory = [...sample.colors];
    assumedStart = Boolean(sample.assumedStart || run.timeline.assumedStart);
    if (sample.finished) { state = 'completed'; run = null; frame = 0; }
    publish(memory, true); controls();
    if (run) frame = requestAnimationFrame(paint);
  }
  function begin(plan, id) {
    if (id !== generation) return;
    if (document.hidden) { stop(); return; }
    clearActivity();
    const timeline = createTimeline(plan, memory);
    lastPlan = plan; lastRevision = revision;
    duration = timeline.durationMs;
    elapsed = 0; assumedStart = Boolean(timeline.assumedStart); state = 'playing'; errorMessage = '';
    const disclosure = stage.closest('details');
    if (disclosure && !disclosure.open) disclosure.open = true;
    if (matchMedia('(max-width: 900px)').matches) stage.scrollIntoView({block: 'center', behavior: 'instant'});
    lastPaint = -Infinity; run = {timeline, origin: performance.now()};
    paint(performance.now(), true);
  }
  async function compiled() {
    if (!pending || pending.revision !== revision) return;
    let sequence;
    try { sequence = JSON.parse(compiledNode.textContent); } catch { return; }
    if (!sequence?.timing || !Array.isArray(sequence.steps) || !sequence.steps.length) return;
    const request = pending; pending = null;
    try {
      // The native /v1/preview response has already resolved colors and
      // compiled the Sequence. Only its canonical Sequence reaches the pinned
      // matching Go planner; this WASM never recompiles colors or Actions.
      const plan = await callCompiler('planSequence', JSON.stringify(sequence));
      if (request.id !== generation || request.revision !== revision) return;
      begin(plan, request.id);
    } catch (error) { fail(error, request.id); }
  }
  function prepare() {
    stop('preparing'); elapsed = 0; duration = 0; assumedStart = false;
    pending = {id: generation, revision};
    const id = generation;
    pendingTimer = setTimeout(() => fail(new Error('The current request did not produce a screen preview.'), id), 12000);
    controls();
  }
  function pause() {
    if (state === 'playing' && run) {
      cancelAnimationFrame(frame); frame = 0;
      paint(performance.now(), true);
      cancelAnimationFrame(frame); frame = 0;
      if (!run) return;
      state = 'paused'; publish(memory, true); controls();
    } else if (state === 'paused' && run) {
      run.origin = performance.now() - elapsed; state = 'playing';
      lastPaint = -Infinity; paint(performance.now(), true);
    }
  }
  buttons.pause.addEventListener('click', pause);
  buttons.stop.addEventListener('click', () => stop());
  buttons.replay.addEventListener('click', () => {
    if (!lastPlan || lastRevision !== revision) return;
    const plan = lastPlan; stop('preparing');
    try { begin(plan, generation); } catch (error) { fail(error, generation); }
  });
  document.addEventListener('click', event => {
    const preview = event.target.closest('#preview');
    if (preview && !preview.disabled) { prepare(); return; }
    const selectedEffect = event.target.closest('[data-kind], [data-action]');
    if (selectedEffect?.getAttribute('aria-pressed') === 'true') return;
    if (event.target.closest('[data-kind], [data-action], .palette button, .editor-tabs button, #use-sequence, #edit-sequence, #sequence-example, #import, #disconnect, #off, #stop, #load-pattern')) stop('idle', true);
  }, true);
  const draftInput = event => {
    if (event.target.closest('#author, #action-fields, .color-card, #sequence-pane, #advanced, #request, #sequence-json, #pattern-select, #device-select')) stop('idle', true);
  };
  document.addEventListener('input', draftInput, true);
  document.addEventListener('change', draftInput, true);
  new MutationObserver(compiled).observe(compiledNode, {childList: true, subtree: true});
  const notice = document.getElementById('play-feedback') || document.getElementById('notice') || document.getElementById('status');
  if (notice) new MutationObserver(() => {
    if (pending && notice.dataset.previewError === 'true' && notice.classList.contains('error')) fail(new Error(notice.textContent), pending.id);
  }).observe(notice, {childList: true, subtree: true, attributes: true, attributeFilter: ['class', 'data-preview-error']});
  new MutationObserver(controls).observe(document.documentElement, {attributes: true, attributeFilter: ['lang']});
  window.addEventListener('nb1-playback-requested', () => stop('idle', true));
  window.addEventListener('nb1-cached-readback', event => {
    // A cached service observation is never a per-command acknowledgment and
    // never interrupts an active screen animation.
    if (['playing', 'paused', 'preparing'].includes(state)) return;
    const colors = event.detail?.colors;
    if (!Array.isArray(colors) || colors.length !== 2) return;
    for (const [index, color] of colors.entries()) {
      if (color === null || /^#[0-9a-f]{6}$/i.test(color || '')) memory[index] = color === null ? null : color.toLowerCase();
    }
  });
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden) return;
    if (state === 'playing') pause();
    else if (state === 'preparing') stop();
  });
  window.addEventListener('pagehide', () => { generation++; clearActivity(); });
  controls();
}
