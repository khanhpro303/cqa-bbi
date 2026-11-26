import assert from 'node:assert/strict';
import test from 'node:test';
import { FloodDetector, RateLimiter, SelfEchoShield } from '../src/chatbot/zalo-message-guards.js';

test('outbound guard enforces spacing and a rolling 20-attempt ceiling independently', () => {
  const limiter = new RateLimiter();
  limiter.recordAttempt(100_000);
  assert.equal(limiter.waitMs(102_999), 1);
  assert.equal(limiter.waitMs(103_000), 0);
  for (let i = 1; i < 20; i++) limiter.recordAttempt(100_000 + i);
  assert.equal(limiter.waitMs(159_999), 1);
  assert.equal(limiter.waitMs(160_000), 0);
});

test('echo matching is scoped to thread, whitespace-normalized, and expires in 30 seconds', () => {
  const shield = new SelfEchoShield();
  shield.recordSent('thread-A', 'Xin chào', 100_000);
  assert.equal(shield.isEcho('thread-A', '  Xin chào  ', 129_999), true);
  assert.equal(shield.isEcho('thread-B', 'Xin chào', 129_999), false);
  assert.equal(shield.isEcho('thread-A', 'Xin chào', 130_000), false);
});

test('sixth inbound in three seconds mutes the sender for 60 seconds', () => {
  const detector = new FloodDetector();
  for (let i = 0; i < 5; i++) assert.equal(detector.isFlooding('sender-A', 100_000 + i), false);
  assert.equal(detector.isFlooding('sender-A', 102_999), true);
  assert.equal(detector.isFlooding('sender-B', 103_000), false);
  assert.equal(detector.isMuted('sender-A', 162_998), true);
  assert.equal(detector.isFlooding('sender-A', 162_999), false);

  const boundary = new FloodDetector();
  for (let i = 0; i < 5; i++) boundary.isFlooding('sender-A', 100_000);
  assert.equal(boundary.isFlooding('sender-A', 103_000), false);
});
