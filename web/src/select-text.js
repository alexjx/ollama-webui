export function selectElementText(element, documentRoot = globalThis.document) {
  const selection = documentRoot?.getSelection?.();
  const range = documentRoot?.createRange?.();
  if (!element || !selection || !range) return false;

  try {
    range.selectNodeContents(element);
    selection.removeAllRanges();
    selection.addRange(range);
    return true;
  } catch {
    return false;
  }
}
