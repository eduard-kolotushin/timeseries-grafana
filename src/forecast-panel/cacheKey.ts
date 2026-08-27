import { isoUtc, rfc3339Utc } from './lookback';
import { ForecastOptions } from './types';

const DROP_KEYS = new Set(['interval', 'intervalMs', 'maxDataPoints']);

export type CacheKeyInput = {
  targets: unknown[];
  visibleFromMs?: number;
  visibleToMs?: number;
  options: ForecastOptions;
  seriesName: string;
};

export function fitOptions(options: ForecastOptions): Record<string, unknown> {
  return {
    model: options.model,
    alpha: options.alpha,
    beta: options.beta,
    period: options.period,
    season: options.season,
    calendar: options.calendar,
  };
}

export function fingerprintPayload(input: CacheKeyInput): unknown {
  return sortValue({
    datasources: datasourceUids(input.targets),
    targets: redactTargets(input.targets, input.visibleFromMs, input.visibleToMs),
    options: fitOptions(input.options),
    trainFrom: input.options.trainRange?.from ?? '',
    trainTo: input.options.trainRange?.to ?? '',
    lookback: input.options.lookback ?? '',
    seriesName: input.seriesName,
  });
}

export async function cacheKey(input: CacheKeyInput): Promise<string> {
  const json = JSON.stringify(fingerprintPayload(input));
  const buf = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(json));
  return [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, '0')).join('');
}

function datasourceUids(targets: unknown[]): string[] {
  const uids = new Set<string>();
  for (const t of targets) {
    if (!t || typeof t !== 'object') {
      continue;
    }
    const ds = (t as { datasource?: { uid?: string } }).datasource;
    if (ds?.uid) {
      uids.add(ds.uid);
    }
  }
  return [...uids].sort();
}

export function redactTargets(targets: unknown, visibleFromMs?: number, visibleToMs?: number): unknown {
  let json = JSON.stringify(targets ?? []);
  const tokens: string[] = [];
  for (const ms of [visibleFromMs, visibleToMs]) {
    if (ms == null || !Number.isFinite(ms)) {
      continue;
    }
    tokens.push(isoUtc(ms), rfc3339Utc(ms), String(ms));
  }
  for (const token of tokens) {
    if (token) {
      json = json.split(token).join('__TIME__');
    }
  }
  return stripWindowFields(JSON.parse(json));
}

function stripWindowFields(v: unknown): unknown {
  if (Array.isArray(v)) {
    return v.map(stripWindowFields);
  }
  if (v && typeof v === 'object') {
    const out: Record<string, unknown> = {};
    for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
      if (DROP_KEYS.has(k)) {
        continue;
      }
      out[k] = stripWindowFields(val);
    }
    return out;
  }
  return v;
}

function sortValue(v: unknown): unknown {
  if (Array.isArray(v)) {
    return v.map(sortValue);
  }
  if (v && typeof v === 'object') {
    const out: Record<string, unknown> = {};
    for (const k of Object.keys(v as object).sort()) {
      out[k] = sortValue((v as Record<string, unknown>)[k]);
    }
    return out;
  }
  return v;
}
