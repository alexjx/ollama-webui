import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import MarkdownContent from '../src/markdown-content.js';
import { WorkspaceLinkContext, resolveWorkspacePath } from '../src/workspace-links.js';
const root = '/srv/workspace';
const render = (source) => renderToStaticMarkup(createElement(WorkspaceLinkContext.Provider,
  { value: { root, open() {} } }, createElement(MarkdownContent, null, source)));

test('normalizes only workspace paths and rejects traversal, schemes and root-prefix collisions', () => {
  for (const [input, expected] of [['/srv/workspace/a.txt', 'a.txt'], ['./资料/结果.csv', '资料/结果.csv'], ['reports/a b.txt', 'reports/a b.txt'], ['/srv/workspace', ''], ['README.md', 'README.md']]) {
    assert.equal(resolveWorkspacePath(input, root, true), expected);
  }
  for (const input of ['/etc/passwd', '/srv/workspace-other/a', '../secret', './a/../../secret', '//evil/a', 'https://example.com/a', 'javascript:alert(1)', 'file:///srv/workspace/a', './a\\b', './a\0b', './a?x', './a#x']) {
    assert.equal(resolveWorkspacePath(input, root, true), null, input);
  }
  assert.equal(resolveWorkspacePath('./a.txt', undefined), null);
});

test('links prose paths, code spans and explicit local links preserving visible path and punctuation', () => {
  const html = render('结果：./资料/结果.csv。 Read /srv/workspace/report.txt, then `reports/my file.txt` or [报告](</srv/workspace/资料/my file.txt>).');
  assert.equal((html.match(/class="workspace-path-link"/g) || []).length, 4);
  assert.match(html, />\.\/资料\/结果.csv<\/a>。/);
  assert.match(html, />\/srv\/workspace\/report.txt<\/a>,/);
  assert.match(html, /<code>reports\/my file.txt<\/code>/);
  assert.match(html, />报告<\/a>/);
  assert.match(html, /data-workspace-path="资料\/my file.txt"/);
});

test('preserves code blocks, external links, images, plain language and raw HTML safety', () => {
  const source = 'and/or 1/2 example.com\n\n```sh\ncat /srv/workspace/a.txt\n```\n\n[external /srv/workspace/a](https://example.com/a)\n\n![image](./image.png)\n\n<script>alert(1)</script>\n\n[bad](javascript:alert%281%29)';
  const html = render(source);
  assert.doesNotMatch(html, /workspace-path-link|<script|href="javascript:/);
  assert.match(html, /cat \/srv\/workspace\/a.txt\n/);
  assert.match(html, /href="https:\/\/example.com\/a"/);
  assert.match(html, /aria-label="Copy code"/);
  assert.match(html, /aria-label="Select code"/);
});

test('reference links resolve once, encoded traversal stays blocked, streamed fragments do not throw', () => {
  const html = render('[file][ref]\n\n[ref]: ./reports/a.txt\n\n[bad](./%2e%2e/secret)');
  assert.equal((html.match(/workspace-path-link/g) || []).length, 1);
  assert.match(html, /data-workspace-path="reports\/a.txt"/);
  for (const value of ['`./reports/', '[file](./reports/', '/srv/workspace/', '```\n./a']) assert.doesNotThrow(() => render(value));
});
