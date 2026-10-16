import assert from "node:assert/strict";
import test from "node:test";
import { isNearTranscriptEnd, scrollToEnd } from "../src/scroll-position.js";

test("scrolls a conversation transcript to its full height without animation", () => {
  const calls = [];
  const transcript = {
    scrollHeight: 960,
    scrollTo(options) { calls.push(options); },
  };

  assert.equal(scrollToEnd(transcript), true);
  assert.deepEqual(calls, [{ top: 960, behavior: "instant" }]);
});

test("falls back to setting scrollTop when scrollTo is unavailable", () => {
  const transcript = { scrollTop: 120, scrollHeight: 960 };

  assert.equal(scrollToEnd(transcript), true);
  assert.equal(transcript.scrollTop, 960);
});

test("does nothing when the transcript is not mounted", () => {
  assert.equal(scrollToEnd(null), false);
});

test("live progress follows the bottom but respects reading earlier messages", () => {
  assert.equal(isNearTranscriptEnd({ scrollHeight: 1000, scrollTop: 550, clientHeight: 400 }), true);
  assert.equal(isNearTranscriptEnd({ scrollHeight: 1000, scrollTop: 100, clientHeight: 400 }), false);
  assert.equal(isNearTranscriptEnd({ scrollHeight: 400, scrollTop: 0, clientHeight: 400 }), true);
  assert.equal(isNearTranscriptEnd(null), false);
});
