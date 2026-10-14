import { createElement, useEffect, useState } from "react";
import { copyText } from "./copy-text.js";

export default function CopyButton({ label, ariaLabel = label, text, getText, onFailure, className, children }) {
  const [feedback, setFeedback] = useState("");
  useEffect(() => {
    if (!feedback) return;
    const timer = setTimeout(() => setFeedback(""), 2400);
    return () => clearTimeout(timer);
  }, [feedback]);

  async function copy() {
    if (await copyText(getText ? getText() : text)) {
      setFeedback("Copied");
    } else {
      onFailure?.();
      setFeedback("Use Select + Ctrl+C / ⌘C");
    }
  }

  return createElement("button", {
    type: "button", className, "aria-label": ariaLabel,
    title: feedback || ariaLabel, onClick: copy,
  }, children, createElement("span", { "aria-live": "polite" }, feedback || label));
}
