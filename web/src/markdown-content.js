import { createElement } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";

const remarkPlugins = [remarkGfm];
const components = {
  img: ({ node, src, ...props }) => src
    ? createElement("img", { ...props, src, loading: "lazy" })
    : createElement("span", null, props.alt),
  table: ({ children }) => createElement("div", {
    className: "markdown-table-scroll", tabIndex: 0, role: "region", "aria-label": "Table",
  }, createElement("table", null, children)),
  pre: ({ children }) => createElement("pre", { tabIndex: 0, "aria-label": "Code block" }, children),
};

export default function MarkdownContent({ children }) {
  // Keep the default URL filtering and HTML escaping for model-generated text.
  return createElement(Markdown, { remarkPlugins, components }, children);
}
