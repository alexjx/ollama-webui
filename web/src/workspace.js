export function workspaceFileURL(path, download = true) {
  return `/api/workspace/file?${new URLSearchParams({ path, download: download ? "1" : "0" })}`;
}

export function workspaceBreadcrumbs(path) {
  const parts = path.split("/").filter(Boolean);
  return [{ name: "Workspace", path: "" }, ...parts.map((name, index) => ({ name, path: parts.slice(0, index + 1).join("/") }))];
}

export function formatFileSize(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  const unit = bytes < 1024 * 1024 ? 1024 : 1024 * 1024;
  return `${(bytes / unit).toFixed(bytes / unit < 10 ? 1 : 0)} ${unit === 1024 ? "KB" : "MB"}`;
}
