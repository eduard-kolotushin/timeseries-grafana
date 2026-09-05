export const DEFAULT_MAX_INFLIGHT_LOADS = 1;

export function clampMaxInflightLoads(n: unknown): number {
  const v = typeof n === 'number' ? n : Number(n);
  if (!Number.isFinite(v) || v < 1) {
    return DEFAULT_MAX_INFLIGHT_LOADS;
  }
  return Math.floor(v);
}

export class OverlayLoadGate {
  private inflight: AbortController[] = [];

  start(max: unknown): AbortController {
    const cap = clampMaxInflightLoads(max);
    while (this.inflight.length >= cap) {
      const oldest = this.inflight.shift();
      oldest?.abort();
    }
    const ac = new AbortController();
    this.inflight.push(ac);
    return ac;
  }

  finish(ac: AbortController): void {
    this.inflight = this.inflight.filter((x) => x !== ac);
  }

  abortAll(): void {
    for (const ac of this.inflight) {
      ac.abort();
    }
    this.inflight = [];
  }
}
