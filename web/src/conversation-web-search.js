import { createElement as h } from "react";

export function conversationWebSearchEnabled(conversation) {
  return conversation?.web_search_enabled !== false;
}

export default function ConversationWebSearch({ mode, enabled, disabled, saving, provider, providerError, error, onChange }) {
  const agent = mode === "agent";
  const hint = !agent ? "Chat talks to the model without tools. Web search is available in Agent mode."
    : providerError ? "Could not refresh Tavily status. Check System settings before using web search."
      : !provider ? "Checking Tavily availability…"
        : !provider.configured ? "Add a Tavily API token and enable web search in System settings. Your choice is saved for this session."
          : !provider.enabled ? "Tavily is disabled in System settings. Enable it there to allow searches in this session."
            : enabled ? "This agent can search the web with Tavily. You can change this between replies."
              : "Web search is off for this session. Other Agent tools remain available.";
  return h("section", { className: "settings-section conversation-web-search" },
    h("label", { className: "web-search-toggle", htmlFor: "conversation-web-search" },
      h("input", {
        id: "conversation-web-search", type: "checkbox", checked: agent && enabled,
        disabled: !agent || disabled || saving,
        "aria-describedby": "conversation-web-search-help",
        onChange: (event) => onChange(event.target.checked),
      }), "Web search"),
    h("p", { id: "conversation-web-search-help" }, hint),
    saving && h("p", { role: "status" }, "Saving web search…"),
    error && h("p", { className: "settings-error", role: "alert" }, error));
}
