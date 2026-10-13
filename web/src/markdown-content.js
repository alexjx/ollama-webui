import { createElement, useRef } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { selectElementText } from "./select-text.js";

function CodeBlock({ children }) {
  const codeRef = useRef(null);

  function selectCode() {
    codeRef.current?.focus({ preventScroll: true });
    selectElementText(codeRef.current);
  }

  return createElement("div", { className: "markdown-code-block" },
    createElement("button", {
      className: "select-code-button",
      type: "button",
      "aria-label": "Select code",
      title: "Select code to copy with Ctrl+C or ⌘C",
      onClick: selectCode,
    }, "Select"),
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

export default function MarkdownContent({ children }) {
  // Keep the default URL filtering and HTML escaping for model-generated text.
  return createElement(Markdown, { remarkPlugins, components }, children);
}
