import { createElement as h, Fragment } from "react";
import { Globe, CircleNotch } from "@phosphor-icons/react";

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
  return h(Fragment, null,
    h("button", {
      type: "button", className: "icon-button composer-search-toggle",
      "aria-label": "Web search", "aria-pressed": agent && enabled,
      "aria-describedby": "conversation-web-search-help",
      disabled: !agent || disabled || saving,
      title: hint, onClick: () => onChange(!enabled),
    }, h(saving ? CircleNotch : Globe, { size: 22, "aria-hidden": true })),
    h("span", { id: "conversation-web-search-help", className: "sr-only" }, hint),
    saving && h("span", { className: "sr-only", role: "status" }, "Saving web search…"),
    error && h("span", { role: "alert" }, error));
}
