function legacyCopy(text, documentRoot) {
  if (!documentRoot?.body || typeof documentRoot.execCommand !== "function") return false;

  const activeElement = documentRoot.activeElement;
  const selection = documentRoot.getSelection?.();
  const ranges = [];
  for (let index = 0; index < (selection?.rangeCount || 0); index++) {
    ranges.push(selection.getRangeAt(index).cloneRange());
  }
  const inputSelection = typeof activeElement?.selectionStart === "number"
    ? [activeElement.selectionStart, activeElement.selectionEnd, activeElement.selectionDirection]
    : null;
  const textarea = documentRoot.createElement("textarea");
  textarea.value = text;
  textarea.readOnly = true;
  textarea.style.cssText = "position:fixed;top:0;left:-9999px;opacity:0;";

  try {
    documentRoot.body.appendChild(textarea);
    textarea.focus({ preventScroll: true });
    textarea.select();
    return documentRoot.execCommand("copy") === true;
  } catch {
    return false;
  } finally {
    textarea.remove();
    try {
      activeElement?.focus?.({ preventScroll: true });
      if (inputSelection) activeElement.setSelectionRange(...inputSelection);
      if (selection) {
        selection.removeAllRanges();
        for (const range of ranges) selection.addRange(range);
      }
    } catch {
      // A copy listener can remove the previously focused/selected element.
    }
  }
}

export async function copyText(text, { navigatorRoot = globalThis.navigator, documentRoot = globalThis.document } = {}) {
  try {
    if (typeof navigatorRoot?.clipboard?.writeText === "function") {
      await navigatorRoot.clipboard.writeText(text);
      return true;
    }
  } catch {
    // Clipboard access can be denied even when the modern API is present.
  }
  // On HTTP custom hostnames this runs directly within the click handler,
  // preserving the user gesture required by the legacy copy command.
  try {
    return legacyCopy(text, documentRoot);
  } catch {
    return false;
  }
}
