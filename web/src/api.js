async function request(path, options = {}) {
  const response = await fetch(path, options);
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`;
    try {
      const payload = await response.json();
      message = payload.error?.message || message;
    } catch {
      // The status text is the best available error.
    }
    const error = new Error(message);
    error.status = response.status;
    throw error;
  }
  if (response.status === 204) return null;
  return response.json();
}

export async function getHealth(signal) {
  return request("/api/health", { signal });
}

export async function listModels(signal) {
  const payload = await request("/api/models", { signal });
  return payload.models || [];
}

export function getSettings(signal) {
  return request("/api/settings", { signal });
}

export async function listConversations(query = "", signal) {
  const params = new URLSearchParams();
  if (query.trim()) params.set("q", query.trim());
  const suffix = params.size ? `?${params}` : "";
  const payload = await request(`/api/conversations${suffix}`, { signal });
  return payload.items || [];
}

export function getConversation(id, signal) {
  return request(`/api/conversations/${id}`, { signal });
}

export function createConversation(input, signal) {
  return request("/api/conversations", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
    signal,
  });
}

export function renameConversation(id, title, signal) {
  return request(`/api/conversations/${id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ title }),
    signal,
  });
}

export function deleteConversation(id, signal) {
  return request(`/api/conversations/${id}`, {
    method: "DELETE",
    signal,
  });
}

export function clearConversations(signal) {
  return request("/api/conversations", {
    method: "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ confirmation: "DELETE" }),
    signal,
  });
}

export async function streamMessage(id, content, { signal, onEvent, images = [] }) {
  const response = await fetch(`/api/conversations/${id}/messages`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ content, images }),
    signal,
  });
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`;
    try {
      const payload = await response.json();
      message = payload.error?.message || message;
    } catch {
      // Preserve the HTTP error when no JSON body is available.
    }
    throw new Error(message);
  }
  if (!response.body) throw new Error("Streaming is unavailable in this browser");

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffered = "";
  while (true) {
    const { done, value } = await reader.read();
    buffered += decoder.decode(value || new Uint8Array(), { stream: !done });
    const lines = buffered.split("\n");
    buffered = lines.pop() || "";
    for (const line of lines) {
      if (line.trim()) onEvent(JSON.parse(line));
    }
    if (done) break;
  }
  if (buffered.trim()) onEvent(JSON.parse(buffered));
}

export async function deleteTurns(id, messageId) {
  // The server may still be saving cancellation after the stream is aborted.
  for (let attempt = 0; ; attempt++) {
    try {
      return await request(`/api/conversations/${id}/messages/${messageId}`, { method: "DELETE" });
    } catch (error) {
      if (error.status !== 409 || attempt >= 8) throw error;
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
  }
}
