const encoder = new TextEncoder();
const decoder = new TextDecoder('utf-8', { fatal: true });
const bytes = (text) => encoder.encode(text || '');
const validDuration = (value) => Number.isFinite(value) && value >= 0;

function freeze(block, now) {
  const timing = block.thinking_timing;
  if (timing?.active) {
    block.duration_ms = timing.duration_ms + Math.max(0, now - timing.received_at_ms);
    block.thinking_timing = { duration_ms: block.duration_ms, active: false };
  }
  block.live = false;
}

export function finalizeActivity(message, now = performance.now()) {
  if (!message.activity) return message;
  const blocks = message.activity.blocks.map((block) => {
    const next = { ...block };
    freeze(next, now);
    return next;
  });
  return { ...message, activity: { ...message.activity, blocks } };
}

// Called before the existing reducers append text or update step/run objects.
export function recordActivityEvent(message, event, now = performance.now()) {
  if (event.type === 'activity.started') {
    if (event.metadata?.activity_version !== 1 || message.activity) return message;
    return { ...message, activity: { version: 1, blocks: [], turn: 1, elapsed: 0, timing_active: false } };
  }
  if (!message.activity) return message;
  if (event.type === 'done') {
    if (event.metadata?.activity) return { ...message, activity: undefined, metadata: { ...message.metadata, activity: event.metadata.activity } };
    return finalizeActivity(message, now);
  }
  const activity = { ...message.activity, blocks: message.activity.blocks.map((block) => ({ ...block })) };
  const blocks = activity.blocks;
  const close = () => blocks.forEach((block) => freeze(block, now));
  if (event.type === 'turn.started') {
    close();
    activity.turn = event.turn || activity.turn;
  }
  if (event.type === 'turn.done') close();
  if (event.type === 'thinking.timing') {
    const elapsed = event.metadata?.thinking_duration_ms;
    if (validDuration(elapsed)) {
      const delta = Math.max(0, elapsed - activity.elapsed);
      const block = blocks.findLast((item) => item.type === 'thinking');
      if (block) {
        // The server's cumulative clock is authoritative; do not add local estimates.
        block.measured_ms = (block.measured_ms || 0) + delta;
        block.duration_ms = block.measured_ms;
        block.thinking_timing = { duration_ms: block.duration_ms, active: event.metadata.thinking_active === true && block.live === true, received_at_ms: now };
      }
      activity.elapsed = elapsed;
      activity.timing_active = event.metadata.thinking_active === true;
      activity.timing_received_at_ms = now;
    }
  }
  const type = event.type === 'thinking.delta' ? 'thinking' : event.type === 'delta' ? 'content' : null;
  if (type && event.content) {
    const turn = event.turn || activity.turn;
    const start = bytes(message[type]).length;
    const end = bytes((message[type] || '') + event.content).length;
    let block = blocks.at(-1);
    if (!block || block.type !== type || block.turn !== turn) {
      close();
      block = { type, turn, start, end, live: true };
      if (type === 'thinking') {
        block.duration_ms = 0;
        block.measured_ms = 0;
        block.thinking_timing = { duration_ms: 0, active: activity.timing_active, received_at_ms: activity.timing_received_at_ms ?? now };
      }
      blocks.push(block);
    } else {
      block.end = end;
      block.live = true;
    }
  }
  const refType = event.type.startsWith('tool.') ? 'tool' : event.type.startsWith('subagent.') ? 'subagent' : null;
  if (refType) {
    const ref = refType === 'tool' ? 'step_id' : 'run_id';
    const id = refType === 'tool' ? event.step?.id : event.run?.id;
    if (id != null && !blocks.some((block) => block.type === refType && block[ref] === id)) {
      close();
      blocks.push({ type: refType, turn: event.turn || activity.turn, [ref]: id });
    }
  }
  return { ...message, activity };
}

// Null means that the caller must use its legacy rendering, preserving all text.
export function readActivityBlocks(message) {
  const saved = message.metadata?.activity;
  const activity = saved ?? message.activity;
  if (activity?.version !== 1 || !Array.isArray(activity.blocks)) return null;
  const source = { thinking: bytes(message.thinking), content: bytes(message.content) };
  const offsets = { thinking: 0, content: 0 };
  const seen = new Set();
  const result = [];
  for (const [index, block] of activity.blocks.entries()) {
    if (!block || !['thinking', 'content', 'tool', 'subagent'].includes(block.type)) return null;
    const resolved = { ...block, key: `activity-${index}`, live: !saved && message.status === 'streaming' && block.live === true };
    if (block.type === 'thinking' || block.type === 'content') {
      const data = source[block.type];
      if (!Number.isInteger(block.start) || !Number.isInteger(block.end) || block.start !== offsets[block.type] || block.end <= block.start || block.end > data.length) return null;
      try { resolved.text = decoder.decode(data.subarray(block.start, block.end)); } catch { return null; }
      offsets[block.type] = block.end;
      if (block.type === 'thinking') {
        resolved.thinking_timing = saved
          ? { duration_ms: validDuration(block.duration_ms) ? block.duration_ms : 0, active: false }
          : block.thinking_timing;
      }
    } else {
      const isTool = block.type === 'tool';
      const id = block[isTool ? 'step_id' : 'run_id'];
      const identity = `${block.type}:${id}`;
      const object = (message[isTool ? 'agent_steps' : 'subagent_runs'] || []).find((item) => item.id === id);
      if (!object || seen.has(identity)) return null;
      seen.add(identity);
      resolved[isTool ? 'step' : 'run'] = object;
    }
    result.push(resolved);
  }
  if (offsets.thinking !== source.thinking.length || offsets.content !== source.content.length) return null;
  for (const [field, type] of [['agent_steps', 'tool'], ['subagent_runs', 'subagent']]) {
    if ((message[field] || []).some((item) => !seen.has(`${type}:${item.id}`))) return null;
  }
  return result;
}
