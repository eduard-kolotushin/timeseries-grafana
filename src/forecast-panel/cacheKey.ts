import { isoUtc, rfc3339Utc } from './lookback';
import { ForecastOptions } from './types';

const DROP_KEYS = new Set(['interval', 'intervalMs', 'maxDataPoints', 'key', 'hide', 'exemplar', 'refId']);

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

function datasourceUid(ds: unknown): string {
  if (!ds || typeof ds !== 'object') {
    return '';
  }
  const uid = (ds as { uid?: string }).uid;
  return typeof uid === 'string' ? uid : '';
}

/** Grafana time macros and interpolated visible-range timestamps become __TIME__. */
function redactTimeTokens(json: string, visibleFromMs?: number, visibleToMs?: number): string {
  let out = json;
  out = out.replace(/\$\{__from(?::[^}]*)?\}/g, '__TIME__');
  out = out.replace(/\$\{__to(?::[^}]*)?\}/g, '__TIME__');
  out = out.replace(/\$__from\b/g, '__TIME__');
  out = out.replace(/\$__to\b/g, '__TIME__');
  for (const ms of [visibleFromMs, visibleToMs]) {
    if (ms == null || !Number.isFinite(ms)) {
      continue;
    }
    for (const token of [isoUtc(ms), rfc3339Utc(ms), String(ms)]) {
      if (token) {
        out = out.split(token).join('__TIME__');
      }
    }
  }
  return out;
}

function normalizeSql(sql: string): string {
  return sql.replace(/\s+/g, ' ').trim();
}

function isSqlBuilder(builder: Record<string, unknown>): boolean {
  const qt = builder.queryType;
  if (qt === 'sql' || qt === 'SQL') {
    return typeof builder.query === 'string';
  }
  return false;
}

function sqlFromTarget(o: Record<string, unknown>): string | undefined {
  const builder = o.builder as Record<string, unknown> | undefined;
  if (builder && isSqlBuilder(builder)) {
    return builder.query as string;
  }
  if (typeof o.rawSql === 'string') {
    return o.rawSql;
  }
  return undefined;
}

function canonicalizeOne(t: unknown): unknown {
  if (!t || typeof t !== 'object') {
    return t;
  }
  const o = t as Record<string, unknown>;
  const uid = datasourceUid(o.datasource);
  const sql = sqlFromTarget(o);
  if (sql != null) {
    return { uid, sql: normalizeSql(sql) };
  }
  if (typeof o.expr === 'string') {
    return { uid, expr: o.expr };
  }
  const rest = { ...o };
  delete rest.datasource;
  return { uid, body: rest };
}

export function redactTargets(targets: unknown, visibleFromMs?: number, visibleToMs?: number): unknown {
  const json = redactTimeTokens(JSON.stringify(targets ?? []), visibleFromMs, visibleToMs);
  const parsed = JSON.parse(json) as unknown;
  const list = Array.isArray(parsed) ? parsed.map(canonicalizeOne) : [canonicalizeOne(parsed)];
  return stripWindowFields(list);
}

function stripWindowFields(v: unknown): unknown {
  if (Array.isArray(v)) {
    return v.map(stripWindowFields);
  }
  if (v && typeof v === 'object') {
    const out: Record<string, unknown> = {};
    for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
      if (DROP_KEYS.has(k) || k === 'datasource') {
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
