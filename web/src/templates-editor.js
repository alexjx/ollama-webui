// Keep number inputs as text while editing; convert only at the API boundary.
export function templateInput(draft) {
  const input = { ...draft, name: draft.name.trim(), settings: draft.settings === null ? null : { ...draft.settings } };
  if (!input.name || (!input.message.trim() && !input.system_prompt.trim())) {
    throw new Error("Enter a name and at least a message or system instructions.");
  }
  for (const [key, label] of [["context_window", "Context window"], ["temperature", "Temperature"]]) {
    if (!input.settings || !(key in input.settings) || input.settings[key] === null) continue;
    const raw = input.settings[key];
    const value = Number(raw);
    if (String(raw).trim() === "" || !Number.isFinite(value)) throw new Error(`${label} needs a number.`);
    if (key === "context_window" && (!Number.isInteger(value) || value < 1)) throw new Error("Context window must be a positive whole number.");
    if (key === "temperature" && (value < 0 || value > 2)) throw new Error("Temperature must be between 0 and 2.");
    input.settings[key] = value;
  }
  return input;
}
