import assert from "node:assert/strict";
import test from "node:test";
import { listWorkspace, previewWorkspaceFile } from "../src/api.js";
import { workspaceFileURL, workspaceBreadcrumbs } from "../src/workspace.js";

test("workspace paths with Unicode and URL punctuation remain exact for browsing, previews and downloads", async (t) => {
  const path = "reports/中文 #1 & final?.txt";
  const controller = new AbortController();
  const calls = [];
  t.mock.method(globalThis, "fetch", async (input, options) => {
    const url = new URL(input, "http://webui.local");
    assert.equal(url.searchParams.get("path"), path);
    assert.equal(options.signal, controller.signal);
    calls.push(url);
    return Response.json({ path, entries: [], nextOffset: null });
  });
  await listWorkspace(path, 500, controller.signal);
  await previewWorkspaceFile(path, controller.signal);
  assert.equal(calls[0].searchParams.get("offset"), "500");
  assert.equal(calls[1].pathname, "/api/workspace/preview");
  for (const download of [true, false]) {
    const url = new URL(workspaceFileURL(path, download), "http://webui.local");
    assert.equal(url.searchParams.get("path"), path);
    assert.equal(url.searchParams.get("download"), download ? "1" : "0");
    assert.equal(url.hash, "");
  }
});

test("workspace breadcrumbs keep root and exact parent paths", () => {
  assert.deepEqual(workspaceBreadcrumbs(""), [{ name: "Workspace", path: "" }]);
  assert.deepEqual(workspaceBreadcrumbs("notes/中文"), [{ name: "Workspace", path: "" }, { name: "notes", path: "notes" }, { name: "中文", path: "notes/中文" }]);
});

test("workspace errors expose a readable reason for retry feedback", async (t) => {
  t.mock.method(globalThis, "fetch", async () => Response.json({ error: { message: "Workspace item no longer exists." } }, { status: 404 }));
  await assert.rejects(previewWorkspaceFile("removed.txt"), (error) => error.status === 404 && error.message === "Workspace item no longer exists.");
});

test("workspace navigation distinguishes a file target from other directory errors", async (t) => {
  for (const [status, code] of [[400, "not_directory"], [400, "invalid_path"], [403, "symlink_not_supported"], [404, "workspace_not_found"]]) {
    const fetch = t.mock.method(globalThis, "fetch", async () => Response.json({ error: { code, message: "Workspace lookup failed." } }, { status }));
    await assert.rejects(listWorkspace("reports/result.txt"), (error) => error.status === status && error.code === code && error.message === "Workspace lookup failed.");
    fetch.mock.restore();
  }
});
