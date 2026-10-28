export const HISTORY_PAGE_SIZE = 5;

// Keep live versions when a page overlaps messages already on screen.
export function prependHistory(current, older) {
  const ids = new Set(current.map((message) => message.id));
  return [...older.filter((message) => !ids.has(message.id)), ...current];
}

export function captureHistoryAnchor(element) {
  if (!element) return null;
  const top = element.getBoundingClientRect().top;
  const message = [...element.querySelectorAll('[data-message-id]')]
    .find((item) => item.getBoundingClientRect().bottom > top);
  return message ? { message, offset: message.getBoundingClientRect().top - top } : null;
}

export function restoreHistoryAnchor(element, anchor) {
  if (!element || !anchor?.message.isConnected) return;
  const offset = anchor.message.getBoundingClientRect().top - element.getBoundingClientRect().top;
  element.scrollTo({ top: element.scrollTop + offset - anchor.offset, behavior: 'instant' });
}
