import { createElement, useEffect, useState } from "react";
import { formatThinkingDuration, thinkingDuration } from "./thinking-duration.js";

export default function ThinkingLabel({ message }) {
  const [now, setNow] = useState(() => performance.now());
  const live = message.status === "streaming" && message.thinking_timing?.active === true;
  const receivedAt = message.thinking_timing?.received_at_ms;
  useEffect(() => {
    if (!live) return;
    setNow(performance.now());
    const timer = setInterval(() => setNow(performance.now()), 1000);
    return () => clearInterval(timer);
  }, [live, receivedAt]);

  const timing = thinkingDuration(message, now);
  return createElement("span", { className: "thinking-duration-label" }, timing
    ? `${timing.live ? "Thinking" : "Thought for"} ${formatThinkingDuration(timing.milliseconds)}`
    : "Model thinking");
}
