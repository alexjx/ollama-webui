function duration(value) {
  return Number.isFinite(value) && value >= 0 ? value : null;
}

export function thinkingDuration(message, now = performance.now()) {
  const timing = message.thinking_timing;
  const saved = duration(message.metadata?.thinking_duration_ms);
  const elapsed = duration(timing?.duration_ms) ?? saved;
  if (elapsed == null) return null;
  const live = message.status === "streaming" && timing?.active === true && duration(timing.received_at_ms) != null;
  return {
    milliseconds: elapsed + (live ? Math.max(0, now - timing.received_at_ms) : 0),
    live,
  };
}

export function applyThinkingTiming(message, metadata, now = performance.now()) {
  const elapsed = duration(metadata?.thinking_duration_ms);
  if (elapsed == null) return message;
  return {
    ...message,
    metadata: { ...message.metadata, thinking_duration_ms: elapsed },
    thinking_timing: { duration_ms: elapsed, active: metadata.thinking_active === true, received_at_ms: now },
  };
}

export function finishThinkingTiming(message, metadata, now = performance.now()) {
  const elapsed = duration(metadata?.thinking_duration_ms) ?? thinkingDuration(message, now)?.milliseconds;
  const result = { ...message, metadata: { ...message.metadata, ...metadata } };
  if (elapsed == null) return result;
  return {
    ...result,
    metadata: { ...result.metadata, thinking_duration_ms: elapsed },
    thinking_timing: { duration_ms: elapsed, active: false },
  };
}

export function formatThinkingDuration(milliseconds) {
  if (duration(milliseconds) == null) return null;
  const seconds = Math.floor(milliseconds / 1000);
  if (seconds < 1) return "<1 s";
  if (seconds < 60) return `${seconds} s`;
  return `${Math.floor(seconds / 60)} min ${seconds % 60} s`;
}
