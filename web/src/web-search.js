export function webSearchStatus(settings) {
  if (!settings) return "Loading…";
  if (!settings.configured) return "Not configured";
  return settings.enabled ? "Enabled" : "Disabled";
}

export function webSearchSettingsInput(enabled, token = "") {
  const input = { enabled: Boolean(enabled) };
  if (token.trim()) input.api_token = token.trim();
  return input;
}

// A settings read started before or during a write must never undo that write.
export function createSettingsRequestGuard() {
  let sequence = 0;
  let revision = 0;
  let writes = 0;
  let writeSequence = 0;
  return {
    read() { return { sequence: ++sequence, revision, duringWrite: writes > 0 }; },
    latest(read) { return read.sequence === sequence; },
    current(read) { return read.sequence === sequence && read.revision === revision && !read.duringWrite; },
    startWrite() { writes++; revision++; return ++writeSequence; },
    currentWrite(write) { return write === writeSequence; },
    finishWrite() { writes--; revision++; },
  };
}

export function parseWebSearchResults(output) {
  let parsed;
  try { parsed = JSON.parse(output); } catch { return null; }
  if (!parsed || !Array.isArray(parsed.results)) return null;
  return {
    query: typeof parsed.query === "string" ? parsed.query : "",
    results: parsed.results.filter((result) => result && typeof result === "object").map((result) => {
      let url = null;
      try {
        const candidate = new URL(result.url);
        if (["http:", "https:"].includes(candidate.protocol) && !candidate.username && !candidate.password) url = candidate.href;
      } catch { /* Invalid or relative URLs stay plain text. */ }
      return {
        title: typeof result.title === "string" && result.title.trim() ? result.title : url || "Search result",
        content: typeof result.content === "string" ? result.content : "",
        url,
      };
    }),
  };
}
