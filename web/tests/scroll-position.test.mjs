import assert from "node:assert/strict";
import test from "node:test";
import { isNearTranscriptEnd, scrollToEnd, shouldFollowTranscript } from "../src/scroll-position.js";

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

test("small upward scrolls escape following while output keeps streaming", () => {
  const transcript = { scrollHeight: 1000, scrollTop: 600, clientHeight: 400 };
  let previousTop = transcript.scrollTop;
  let following = true;
  for (let index = 0; index < 5; index++) {
    transcript.scrollTop -= 10;
    following = shouldFollowTranscript(transcript, previousTop, following);
    previousTop = transcript.scrollTop;
    transcript.scrollHeight += 5;
    following = shouldFollowTranscript(transcript, previousTop, following);
    assert.equal(following, false);
  }
  assert.equal(transcript.scrollTop, 550);
});

test("following resumes only when scrolling downward near the end", () => {
  const transcript = { scrollHeight: 1000, scrollTop: 300, clientHeight: 400 };
  assert.equal(shouldFollowTranscript(transcript, 280, false), false);
  transcript.scrollTop = 580;
  assert.equal(shouldFollowTranscript(transcript, 300, false), true);
  assert.equal(shouldFollowTranscript(transcript, 600, true), false);
  assert.equal(shouldFollowTranscript(transcript, 580, false), false);
  assert.equal(shouldFollowTranscript(transcript, 580, true), true);
  assert.equal(shouldFollowTranscript(null, 0, true), false);
});

test("viewport resizing can clamp scrollTop upward while still following the end", () => {
  assert.equal(shouldFollowTranscript({ scrollHeight: 1000, scrollTop: 400, clientHeight: 600 }, 600, true), true);
  assert.equal(shouldFollowTranscript({ scrollHeight: 1000, scrollTop: 399, clientHeight: 600 }, 600, true), false);
});
