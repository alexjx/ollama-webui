import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import MarkdownContent from '../src/markdown-content.js';

const render = (text) => renderToStaticMarkup(createElement(MarkdownContent, null, text));

test('renders common Markdown and GitHub extensions as semantic elements', () => {
  const html = render('# Heading\n\n**Bold**, *italic*, ~~removed~~ and `inline`\n\n> Quote\n\n1. First\n2. Second\n\n- [x] Done\n- [ ] Pending\n\n| Name | Value |\n| --- | ---: |\n| a | 1 |\n\n```js\nconst x = 1;\n```\n\n[Link](https://example.com)\n\n---\n\nNote[^1]\n\n[^1]: Footnote');
  for (const part of ['<h1>Heading</h1>', '<strong>Bold</strong>', '<em>italic</em>', '<del>removed</del>', '<code>inline</code>', '<blockquote>', '<ol>', 'type="checkbox"', 'checked=""', '<table>', '<th', 'text-align:right', 'language-js', 'href="https://example.com"', '<hr', 'Footnote']) assert.ok(html.includes(part), part);
});

test('escapes raw HTML and filters executable URLs', () => {
  const html = render('<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\n[Bad](javascript:alert%281%29)\n\n![Bad](data:text/html,evil)');
  assert.doesNotMatch(html, /<script|<img[^>]+onerror|href="javascript:|src="data:/);
  assert.match(html, /&lt;script&gt;/);
});

test('handles incomplete streamed Markdown and then completed syntax', () => {
  for (const input of ['**bo', '> quo', '```js\nconst x =', '| a | b |\n| ---', '[link](https://']) assert.doesNotThrow(() => render(input));
  assert.match(render('**bold**'), /<strong>bold<\/strong>/);
  assert.match(render('```js\nconst x = 1;\n```'), /const x = 1;/);
});

test('preserves code whitespace and provides keyboard access to scrolling regions', () => {
  const html = render('```\n  spaced\n    indented\n```\n\n| a | b |\n| - | - |\n| 1 | 2 |');
  assert.match(html, /  spaced\n    indented\n/);
  assert.match(html, /<pre tabindex="0"/);
  assert.match(html, /role="region" aria-label="Table"/);
});
