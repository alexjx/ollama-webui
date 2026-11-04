export const HISTORY_PAGE_SIZE = 5;
export const HISTORY_RETAIN_MESSAGES = 30;

// Release whole pages above the reader, keeping a page of scroll-back buffer.
// Reading older history may temporarily exceed the target; never remove a
// visible, focused, selected, or unsaved message just to meet that target.
export function releaseHistory(current, visibleId, protectedIds = []) {
  const visibleIndex = current.findIndex((message) => String(message.id) === String(visibleId));
  if (visibleIndex < 0) return current;
  let removable = Math.min(current.length - HISTORY_RETAIN_MESSAGES, visibleIndex - HISTORY_PAGE_SIZE);
  const protectedIndex = current.findIndex((message) => protectedIds.includes(String(message.id))
    || typeof message.id !== 'number' || message.status === 'streaming');
  if (protectedIndex >= 0) removable = Math.min(removable, protectedIndex);
  const count = Math.floor(removable / HISTORY_PAGE_SIZE) * HISTORY_PAGE_SIZE;
  return count > 0 ? current.slice(count) : current;
}

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
