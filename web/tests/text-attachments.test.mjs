import assert from "node:assert/strict";
import test from "node:test";
import {
  attachmentMediaType, fileFromLargePaste, largePasteBytes, maxAttachmentBytes,
  readAttachment, splitAttachments, validateAttachmentFiles,
} from "../src/text-attachments.js";
import { streamMessage } from "../src/api.js";

test("converts only pastes above the UTF-8 byte threshold into text files", async () => {
  assert.equal(fileFromLargePaste("small paste"), null);
  assert.equal(fileFromLargePaste("x".repeat(largePasteBytes)), null);
  const text = "🙂".repeat(4097);
  assert.ok(text.length < largePasteBytes);
  const file = fileFromLargePaste(text, 2);
  assert.equal(file.name, "pasted-text-2.txt");
  assert.equal(file.type, "text/plain");
  assert.equal(file.size, Buffer.byteLength(text));
  assert.equal(await file.text(), text);
});

test("preserves leading spaces, Unicode, CRLF, and trailing newlines in pasted files", async () => {
  const text = "  用户输入 🙂\r\n\tsecond line\n".repeat(1000) + "\n";
  const file = fileFromLargePaste(text);
  assert.deepEqual(Buffer.from(await file.arrayBuffer()), Buffer.from(text));
});

test("accepts text without vision and retains image capability checks", () => {
  const text = new File(["content"], "notes.txt");
  assert.equal(attachmentMediaType(text), "text/plain");
  assert.doesNotThrow(() => validateAttachmentFiles([text]));
  const image = new File(["image"], "a.png", { type: "image/png" });
  assert.throws(() => validateAttachmentFiles([image]), /vision-capable/);
  assert.doesNotThrow(() => validateAttachmentFiles([image, text], [], true));
  assert.throws(() => validateAttachmentFiles([new File(["binary"], "a.exe")] ), /UTF-8 text/);
});

test("enforces combined file count and size limits without mutating selected files", () => {
  const selected = Array.from({ length: 4 }, () => ({ size: 1 }));
  assert.throws(() => validateAttachmentFiles([new File(["text"], "notes.txt")], selected), /at most 4/);
  assert.equal(selected.length, 4);
  assert.throws(() => validateAttachmentFiles([new File([], "empty.txt")]), /1 byte/);
  assert.throws(() => validateAttachmentFiles([{ name: "huge.txt", type: "text/plain", size: maxAttachmentBytes + 1 }]), /10 MiB/);
  const files = Array.from({ length: 3 }, () => ({ name: "large.txt", type: "text/plain", size: 8 * 1024 * 1024 }));
  assert.throws(() => validateAttachmentFiles(files), /20 MiB/);
});

test("rejects invalid UTF-8 before preparing a text upload", async () => {
  await assert.rejects(readAttachment(new File([new Uint8Array([0xff])], "invalid.txt", { type: "text/plain" })), /UTF-8/);
  await assert.rejects(readAttachment(new File(["contains\0NUL"], "invalid.txt", { type: "text/plain" })), /NUL/);
});

test("rejects text file names the server cannot preserve", () => {
  for (const name of ["../notes.txt", "folder\\notes.txt", "bad\nname.txt", "用".repeat(60) + ".txt"]) {
    assert.throws(() => validateAttachmentFiles([new File(["content"], name, { type: "text/plain" })]), /file names/);
  }
  assert.doesNotThrow(() => validateAttachmentFiles([new File(["content"], "用户笔记.txt", { type: "text/plain" })]));
});

test("reads browser text files exactly and keeps repeated attachment IDs unique", async (t) => {
  const originalReader = globalThis.FileReader;
  globalThis.FileReader = class {
    async readAsDataURL(file) {
      const bytes = Buffer.from(await file.arrayBuffer());
      this.result = `data:${file.type || "application/octet-stream"};base64,${bytes.toString("base64")}`;
      this.onload();
    }
  };
  t.after(() => {
    if (originalReader) globalThis.FileReader = originalReader;
    else delete globalThis.FileReader;
  });
  const text = "  preserved\r\n用户输入🙂\n";
  const file = new File([text], "notes.txt");
  const first = await readAttachment(file);
  const second = await readAttachment(file);
  assert.notEqual(first.id, second.id);
  assert.equal(first.media_type, "text/plain");
  assert.equal(Buffer.from(first.data, "base64").toString("utf8"), text);
});

test("sends browser-created text as files alongside unchanged image requests", async (t) => {
  const text = "x".repeat(20000);
  const file = { name: "pasted-text-1.txt", media_type: "text/plain", data: Buffer.from(text).toString("base64") };
  const image = { name: "pixel.png", media_type: "image/png", data: "image-base64" };
  const attachments = splitAttachments([file, image]);
  t.mock.method(globalThis, "fetch", async (path, options) => {
    assert.equal(path, "/api/conversations/7/messages");
    const body = JSON.parse(options.body);
    assert.equal(body.content, "Review this file");
    assert.deepEqual(body.images, [image]);
    assert.deepEqual(body.files, [file]);
    assert.equal(Buffer.from(body.files[0].data, "base64").toString("utf8"), text);
    return new Response('{"type":"done"}\n');
  });
  const events = [];
  await streamMessage(7, "Review this file", { ...attachments, onEvent: event => events.push(event) });
  assert.deepEqual(events, [{ type: "done" }]);
});
