import { createElement, useContext, useRef } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { selectElementText } from "./select-text.js";
import { WorkspaceLinkContext, remarkWorkspaceLinks } from "./workspace-links.js";
import CopyButton from "./copy-button.js";

function CodeBlock({ children }) {
  const codeRef = useRef(null);

  function selectCode() {
    codeRef.current?.focus({ preventScroll: true });
    selectElementText(codeRef.current);
  }

  return createElement("div", { className: "markdown-code-block" },
    createElement("div", { className: "markdown-code-actions" },
      createElement(CopyButton, {
        label: "Copy", ariaLabel: "Copy code", className: "select-code-button",
        getText: () => codeRef.current?.textContent || "", onFailure: selectCode,
      }),
      createElement("button", {
        className: "select-code-button",
        type: "button",
        "aria-label": "Select code",
        title: "Select code to copy with Ctrl+C or ⌘C",
        onClick: selectCode,
      }, "Select")),
    createElement("pre", { ref: codeRef, tabIndex: 0, "aria-label": "Code block" }, children),
  );
}

const remarkPlugins = [remarkGfm];
const components = {
  img: ({ node, src, ...props }) => src
    ? createElement("img", { ...props, src, loading: "lazy" })
    : createElement("span", null, props.alt),
  table: ({ children }) => createElement("div", {
    className: "markdown-table-scroll", tabIndex: 0, role: "region", "aria-label": "Table",
  }, createElement("table", null, children)),
  pre: CodeBlock,
};

function WorkspaceAnchor({ node, children, ...props }) {
  const workspace = useContext(WorkspaceLinkContext);
  const path = node.properties["data-workspace-path"];
  if (!workspace || typeof path !== "string") return createElement("a", props, children);
  return createElement("a", { ...props, className: "workspace-path-link",
    title: "Open in Workspace",
    onClick: (event) => {
      if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
      const selection = window.getSelection();
      // Drag-selecting a path must not open the dialog; keyboard activation still works.
      if (event.detail > 0 && selection?.toString() && selection.containsNode(event.currentTarget, true)) {
        event.preventDefault();
        return;
      }
      event.preventDefault();
      workspace.open(path, event.currentTarget);
    },
  }, children);
}

const linkedComponents = { ...components, a: WorkspaceAnchor };

export default function MarkdownContent({ children }) {
  const workspace = useContext(WorkspaceLinkContext);
  // Keep default URL filtering/HTML escaping; only transform the display tree.
  return createElement(Markdown, {
    remarkPlugins: workspace ? [...remarkPlugins, [remarkWorkspaceLinks, { root: workspace.root }]] : remarkPlugins,
    components: workspace ? linkedComponents : components,
  }, children);
}
