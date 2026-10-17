import { createContext } from "react";

export const WorkspaceLinkContext = createContext(null);

// Only absolute paths rooted in the configured workspace become links.
export function resolveWorkspacePath(value, root) {
  if (!root || typeof value !== "string" || !value || value !== value.trim()) return null;
  if (/[\x00-\x1f\x7f\\?#]/.test(value) || /^[a-z][a-z\d+.-]*:/i.test(value) || value.startsWith("//")) return null;
  const base = root.replace(/\/+$/, "");
  if (!value.startsWith("/") || (value !== base && !value.startsWith(`${base}/`))) return null;
  const path = value.slice(base.length).replace(/^\//, "");
  const parts = path.split("/");
  if (parts.includes("..")) return null;
  return parts.filter((part) => part && part !== ".").join("/");
}

function pathLink(path, children) {
  return { type: "link", url: `#workspace=${encodeURIComponent(path)}`, children,
    data: { hProperties: { "data-workspace-path": path } } };
}

export function remarkWorkspaceLinks({ root }) {
  return (tree) => {
    const definitions = new Map();
    function collect(node) {
      if (node.type === "definition") definitions.set(node.identifier, node.url);
      node.children?.forEach(collect);
    }
    collect(tree);
    function walk(parent) {
      if (!parent.children || ["code", "html", "image", "imageReference"].includes(parent.type)) return;
      parent.children = parent.children.flatMap((node) => {
        if (node.type === "link" || node.type === "linkReference") {
          let url = node.url ?? definitions.get(node.identifier);
          try { url = decodeURIComponent(url); } catch { return [node]; }
          const path = resolveWorkspacePath(url, root);
          return [path === null ? node : { ...pathLink(path, node.children), title: node.title }];
        }
        if (node.type === "inlineCode") {
          const path = resolveWorkspacePath(node.value, root);
          return [path === null ? node : pathLink(path, [node])];
        }
        if (node.type !== "text") { walk(node); return [node]; }
        const result = [];
        // Tokenize prose conservatively; whitespace-containing paths belong in
        // a code span or explicit Markdown link to avoid guessing boundaries.
        const tokens = /[^\s<>"'`，。；：！？（）【】()\[\]{}]+/gu;
        let end = 0;
        for (const match of node.value.matchAll(tokens)) {
          const candidate = match[0].replace(/[.,;!:)\]}]+$/, "");
          if (!candidate.startsWith("/")) continue;
          const path = resolveWorkspacePath(candidate, root);
          if (path === null) continue;
          result.push({ type: "text", value: node.value.slice(end, match.index) });
          result.push(pathLink(path, [{ type: "text", value: candidate }]));
          end = match.index + candidate.length;
        }
        if (!result.length) return [node];
        result.push({ type: "text", value: node.value.slice(end) });
        return result;
      });
    }
    walk(tree);
  };
}
