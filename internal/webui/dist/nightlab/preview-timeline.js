/* Pure screen study of a Go nb1-sequence-playback/v1 plan.
 * No clock, DOM, device transport, Jobs or reports live in this module.
 * RGB interpolation is illustrative, not a measurement of physical light.
 */
const CONTRACT = 'nb1-sequence-playback/v1';
const HEX = /^#[0-9a-f]{6}$/i;

function milliseconds(value, name) {
  if (!Number.isSafeInteger(value) || value < 0) throw new TypeError(`Invalid ${name}`);
  return value;
}

function color(value, name) {
  if (typeof value !== 'string' || !HEX.test(value)) throw new TypeError(`Invalid ${name}`);
  return value.toLowerCase();
}

function rgb(hex) {
  return [1, 3, 5].map(index => parseInt(hex.slice(index, index + 2), 16));
}

function hex(channels) {
  return '#' + channels.map(value => Math.round(value).toString(16).padStart(2, '0')).join('');
}

function upperBound(items, time, value) {
  let low = 0, high = items.length;
  while (low < high) {
    const middle = (low + high) >>> 1;
    if (value(items[middle]) <= time) low = middle + 1;
    else high = middle;
  }
  return low;
}

function channelAt(channel, time) {
  const index = upperBound(channel.segments, time, segment => segment.at) - 1;
  if (index < 0) return channel.initial;
  const segment = channel.segments[index];
  if (!segment.fadeMs || time >= segment.at + segment.fadeMs) return segment.to;
  const progress = Math.max(0, (time - segment.at) / segment.fadeMs);
  return segment.from.map((value, index) => value + (segment.to[index] - value) * progress);
}

function freezeChannel(channel) {
  if (channel.initial) Object.freeze(channel.initial);
  for (const segment of channel.segments) {
    Object.freeze(segment.from);
    Object.freeze(segment.to);
    Object.freeze(segment);
  }
  Object.freeze(channel.segments);
  return Object.freeze(channel);
}

/**
 * Turn an already validated Go playback plan into an immutable screen timeline.
 * Initial writes run once; repeat is already expanded by Go. Off is the plan's
 * final LED 0 write, rather than a new step or an invented per-cycle cleanup.
 * durationMs includes finish waits, which Go excludes from nominal_duration_ms.
 * Unknown channels stay null until addressed. A first positive fade with an
 * unknown origin uses black solely as a declared screen-preview assumption.
 */
export function createTimeline(plan, initialColors = [null, null]) {
  if (plan?.contract !== CONTRACT || !['serial', 'interval'].includes(plan.timing) ||
      !['hold', 'off'].includes(plan.on_finish) || !Array.isArray(plan.initial) ||
      plan.initial.length > 2 || !Array.isArray(plan.steps) || !plan.steps.length ||
      plan.steps.length > 1024 || !Array.isArray(plan.finish) || plan.finish.length > 1 ||
      plan.total_steps !== plan.steps.length) throw new TypeError('Invalid Go playback plan');
  if (!Array.isArray(initialColors) || initialColors.length !== 2) throw new TypeError('Expected two initial colors');
  const channels = initialColors.map((value, index) => ({
    initial: value === null ? null : rgb(color(value, `initial color ${index + 1}`)),
    segments: [], assumedAt: null,
  }));
  const nominalDurationMs = milliseconds(plan.nominal_duration_ms, 'nominal duration');
  const events = [], starts = [];
  let order = 0, end = 0, lastStart = -1;

  function write(target, cursor) {
    if (!target || ![0, 1, 2].includes(target.led)) throw new TypeError('Invalid target LED');
    const at = cursor + milliseconds(target.wait_before_ms, 'target wait before');
    const fadeMs = milliseconds(target.fade_ms, 'target fade');
    events.push({at, fadeMs, led: target.led, to: rgb(color(target.color, 'target color')), order: order++});
    return {at, next: at + milliseconds(target.wait_after_ms, 'target wait after')};
  }

  let cursor = 0;
  for (const target of plan.initial) cursor = write(target, cursor).next;
  end = cursor;
  for (const [index, step] of plan.steps.entries()) {
    const start = milliseconds(step.nominal_offset_ms, 'step offset');
    if (step.index !== index || start < lastStart || !Array.isArray(step.targets) ||
        !step.targets.length || step.targets.length > 2) throw new TypeError('Invalid playback step');
    lastStart = start;
    starts.push(start);
    cursor = start;
    let lastWrite = start;
    for (const target of step.targets) {
      const written = write(target, cursor);
      lastWrite = written.at;
      cursor = written.next;
    }
    const waitAfter = milliseconds(step.wait_after_ms, 'step wait after');
    const deadline = milliseconds(step.deadline_ms, 'step deadline');
    const settle = milliseconds(step.settle_after_ms, 'step settle');
    end = Math.max(end, plan.timing === 'serial'
      ? cursor + waitAfter
      : Math.max(cursor, start + deadline, lastWrite + settle));
  }
  cursor = Math.max(end, nominalDurationMs);
  if ((plan.on_finish === 'hold' && plan.finish.length) ||
      (plan.on_finish === 'off' && (plan.finish.length !== 1 || plan.finish[0].led !== 0 ||
        plan.finish[0].color?.toLowerCase() !== '#000000' || plan.finish[0].fade_ms !== 0))) {
    throw new TypeError('Invalid playback finish');
  }
  for (const target of plan.finish) cursor = write(target, cursor).next;

  events.sort((first, second) => first.at - second.at || first.order - second.order);
  for (const event of events) {
    const selected = event.led === 0 ? [0, 1] : [event.led - 1];
    for (const index of selected) {
      const channel = channels[index];
      let from = channelAt(channel, event.at);
      if (from === null && event.fadeMs > 0) {
        from = [0, 0, 0];
        if (channel.assumedAt === null) channel.assumedAt = event.at;
      }
      channel.segments.push({at: event.at, fadeMs: event.fadeMs, from, to: [...event.to]});
    }
  }
  return Object.freeze({
    contract: CONTRACT,
    durationMs: cursor,
    nominalDurationMs,
    total: starts.length,
    assumedStart: channels.some(channel => channel.assumedAt !== null),
    channels: Object.freeze(channels.map(freezeChannel)),
    starts: Object.freeze(starts),
  });
}

/**
 * Sample from time zero, independently of earlier samples or animation frames.
 * step is 1-based, or 0 while initial writes precede the first step.
 * assumedStart stays true after an assumed fade, so its origin stays disclosed.
 */
export function sampleTimeline(timeline, elapsedMs) {
  if (timeline?.contract !== CONTRACT || !Number.isFinite(elapsedMs)) throw new TypeError('Invalid timeline sample');
  const time = Math.min(timeline.durationMs, Math.max(0, elapsedMs));
  const assumedChannels = timeline.channels.map(channel => channel.assumedAt !== null && time >= channel.assumedAt);
  return {
    colors: timeline.channels.map(channel => {
      const value = channelAt(channel, time);
      return value === null ? null : hex(value);
    }),
    finished: elapsedMs >= timeline.durationMs,
    durationMs: timeline.durationMs,
    step: upperBound(timeline.starts, time, value => value),
    total: timeline.total,
    assumedStart: assumedChannels.some(Boolean),
    assumedChannels,
  };
}
