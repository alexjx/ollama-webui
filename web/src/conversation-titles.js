// Poll results update naming only: a newer turn's model, counts, or ordering
// must not be replaced by a snapshot taken while the title was generated.
function titleTime(value = "") {
  // Server timestamps are UTC RFC3339 with optional nanoseconds. Pad the
  // fraction before comparing so same-millisecond edits keep their order.
  return value.replace(/(?:\.(\d+))?Z$/, (_, fraction = "") => `.${fraction.padEnd(9, "0")}Z`);
}

export function newestConversationTitle(conversation, recent) {
  return recent && titleTime(recent.updated_at) >= titleTime(conversation.updated_at) ? recent : conversation;
}

export function mergeConversationTitles(items, refreshed) {
  const byID = new Map(refreshed.map((item) => [item.id, item]));
  return items.map((item) => {
    const updated = byID.get(item.id);
    if (!item.auto_title_pending || !updated || updated.auto_title_pending) return item;
    if (newestConversationTitle(item, updated) !== updated) return item;
    return { ...item, title: updated.title, auto_title_pending: false };
  });
}
