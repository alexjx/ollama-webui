// Poll results update naming only: a newer turn's model, counts, or ordering
// must not be replaced by a snapshot taken while the title was generated.
export function mergeConversationTitles(items, refreshed) {
  const byID = new Map(refreshed.map((item) => [item.id, item]));
  return items.map((item) => {
    const updated = byID.get(item.id);
    if (!item.auto_title_pending || !updated || updated.auto_title_pending) return item;
    if (updated.updated_at < item.updated_at) return item;
    return { ...item, title: updated.title, auto_title_pending: false };
  });
}
