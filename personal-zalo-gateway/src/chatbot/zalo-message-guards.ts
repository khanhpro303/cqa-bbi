import { setTimeout as delay } from 'node:timers/promises';

// Matches the Zalo Flow safeguards for automated sends and inbound replies.
const minuteMs = 60_000;
const minimumSendIntervalMs = 3_000;
const maxSendsPerMinute = 20;
const echoTtlMs = 30_000;
const floodWindowMs = 3_000;
const maxInboundInWindow = 5;
const floodMuteMs = 60_000;

export class RateLimiter {
  private sentAt: number[] = [];
  private queue: Promise<void> = Promise.resolve();

  waitMs(now = Date.now()): number {
    this.sentAt = this.sentAt.filter((time) => now - time < minuteMs);
    const last = this.sentAt.at(-1);
    const intervalWait = last === undefined ? 0 : last + minimumSendIntervalMs - now;
    const minuteWait = this.sentAt.length < maxSendsPerMinute ? 0 : this.sentAt[0]! + minuteMs - now;
    return Math.max(0, intervalWait, minuteWait);
  }

  recordAttempt(now = Date.now()): void {
    this.sentAt.push(now);
  }

  async schedule(send: (recordAttempt: () => void) => Promise<void>): Promise<void> {
    const job = this.queue.then(async () => {
      for (let wait = this.waitMs(); wait > 0; wait = this.waitMs()) {
        await delay(wait);
      }
      await send(() => this.recordAttempt());
    });
    this.queue = job.catch(() => undefined);
    await job;
  }
}

export class SelfEchoShield {
  private readonly sent = new Map<string, number>();

  recordSent(threadId: string, text: string, now = Date.now()): void {
    for (const [key, expiresAt] of this.sent) {
      if (expiresAt <= now) this.sent.delete(key);
    }
    this.sent.set(JSON.stringify([threadId, text.trim()]), now + echoTtlMs);
  }

  isEcho(threadId: string, text: string, now = Date.now()): boolean {
    return (this.sent.get(JSON.stringify([threadId, text.trim()])) ?? 0) > now;
  }
}

interface SenderActivity {
  timestamps: number[];
  mutedUntil: number;
}

export class FloodDetector {
  private readonly senders = new Map<string, SenderActivity>();
  private nextCleanupAt = 0;

  isMuted(senderId: string, now = Date.now()): boolean {
    return (this.senders.get(senderId)?.mutedUntil ?? 0) > now;
  }

  isFlooding(senderId: string, now = Date.now()): boolean {
    if (now >= this.nextCleanupAt) {
      for (const [id, activity] of this.senders) {
        if (activity.mutedUntil <= now && !activity.timestamps.some((time) => now - time < floodWindowMs)) {
          this.senders.delete(id);
        }
      }
      this.nextCleanupAt = now + minuteMs;
    }
    if (this.isMuted(senderId, now)) return true;
    const activity = this.senders.get(senderId) ?? { timestamps: [], mutedUntil: 0 };
    activity.timestamps = activity.timestamps.filter((time) => now - time < floodWindowMs);
    activity.timestamps.push(now);
    this.senders.set(senderId, activity);
    if (activity.timestamps.length <= maxInboundInWindow) return false;

    activity.mutedUntil = now + floodMuteMs;
    activity.timestamps = [];
    return true;
  }
}
