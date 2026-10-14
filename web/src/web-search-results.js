import { createElement as h } from "react";
import { parseWebSearchResults } from "./web-search.js";

export default function WebSearchResults({ output }) {
  const search = parseWebSearchResults(output);
  if (!search) return h("pre", null, output);
  if (!search.results.length) return h("div", { className: "web-search-results" }, h("p", null, "No search results found."));
  return h("ol", { className: "web-search-results", "aria-label": "Web search results" }, search.results.map((result, index) =>
    h("li", { key: index },
      result.url
        ? h("a", { href: result.url, target: "_blank", rel: "noopener noreferrer", "aria-label": `${result.title} (opens in a new tab)` }, result.title)
        : h("strong", null, result.title),
      result.content && h("p", null, result.content))));
}
