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

// Small upward gestures must escape following even inside the bottom threshold.
// An unchanged position (including a delayed programmatic scroll event) keeps
// the current choice; only downward scrolling near the end resumes following.
export function shouldFollowTranscript(element, previousTop, following) {
  if (!element || element.scrollTop < previousTop) return false;
  if (element.scrollTop > previousTop) return isNearTranscriptEnd(element);
  return following;
}
