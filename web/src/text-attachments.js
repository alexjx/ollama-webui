export const largePasteBytes = 16 * 1024;
export const maxAttachmentBytes = 10 * 1024 * 1024;
export const maxTotalAttachmentBytes = 20 * 1024 * 1024;
const imageTypes = new Set(["image/jpeg", "image/png", "image/webp"]);
let nextAttachmentID = 0;

export function isImageAttachment(attachment) {
  return attachment.media_type?.startsWith("image/") || false;
}

export function attachmentMediaType(file) {
  return file.type || (file.name.toLowerCase().endsWith(".txt") ? "text/plain" : "");
}

export function fileFromLargePaste(text, number = 1) {
  const file = new File([text], `pasted-text-${number}.txt`, { type: "text/plain", endings: "transparent" });
  return file.size > largePasteBytes ? file : null;
}

export function validateAttachmentFiles(files, existing = [], supportsImages = false) {
  if (existing.length + files.length > 4) throw new Error("Attach at most 4 files per message.");
  for (const file of files) {
    const type = attachmentMediaType(file);
    if (type !== "text/plain" && !imageTypes.has(type)) throw new Error("Attach UTF-8 text (.txt), JPEG, PNG, or WebP files.");
    if (type === "text/plain" && (!file.name.trim() || new TextEncoder().encode(file.name).length > 180 || file.name === "." || file.name === ".." || file.name.includes("/") || file.name.includes("\\") || /[\u0000-\u001f\u007f-\u009f]/u.test(file.name))) {
      throw new Error("Text file names must be at most 180 bytes and contain no path separators or control characters.");
    }
    if (imageTypes.has(type) && !supportsImages) throw new Error("Choose a vision-capable model to attach images.");
    if (file.size === 0 || file.size > maxAttachmentBytes) throw new Error("Each file must be between 1 byte and 10 MiB.");
  }
  const total = [...existing, ...files].reduce((sum, file) => sum + file.size, 0);
  if (total > maxTotalAttachmentBytes) throw new Error("Attachments may total at most 20 MiB per message.");
}

export async function readAttachment(file) {
  const mediaType = attachmentMediaType(file);
  if (mediaType === "text/plain") {
    let text;
    try {
      text = new TextDecoder("utf-8", { fatal: true }).decode(await file.arrayBuffer());
    } catch {
      throw new Error(`${file.name} must contain UTF-8 text.`);
    }
    if (text.includes("\0")) throw new Error(`${file.name} must contain plain text without NUL characters.`);
  }
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(new Error(`Could not read ${file.name}`));
    reader.onload = () => {
      const url = String(reader.result);
      resolve({
        id: `attachment-${++nextAttachmentID}`,
        name: file.name, file_name: file.name, media_type: mediaType, size: file.size,
        data: url.slice(url.indexOf(",") + 1), url,
      });
    };
    reader.readAsDataURL(file);
  });
}

export function splitAttachments(attachments) {
  const images = [];
  const files = [];
  for (const attachment of attachments) {
    const { name, media_type, data } = attachment;
    (isImageAttachment(attachment) ? images : files).push({ name, media_type, data });
  }
  return { images, files };
}
