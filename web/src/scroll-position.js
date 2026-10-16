export function scrollToEnd(element) {
  if (!element) return false;
  if (typeof element.scrollTo === "function") {
    element.scrollTo({ top: element.scrollHeight, behavior: "instant" });
  } else {
    element.scrollTop = element.scrollHeight;
  }
  return true;
}

export function isNearTranscriptEnd(element, threshold = 64) {
  return !!element && element.scrollHeight - element.scrollTop - element.clientHeight <= threshold;
}
