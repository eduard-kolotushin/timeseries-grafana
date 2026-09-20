import {
  applyLookbackRange,
  isoUtc,
  rfc3339Utc,
  trainStepInterval,
} from './lookback';
import {
  REASON_UNSUPPORTED_OS_QUERY,
  REASON_UNSUPPORTED_PG_FORMAT,
  REASON_UNSUPPORTED_PROM_INSTANT,
} from './reasons';

export const DS_PROMETHEUS = 'prometheus';
export const DS_OPENSEARCH = 'grafana-opensearch-datasource';
export const DS_POSTGRES = 'postgres';
export const DS_POSTGRES_PLUGIN = 'grafana-postgresql-datasource';
export const DS_DRUID = 'grafadruid-druid-datasource';

const PG_TIME_MACROS = [
  '$__timeFilter',
  '$__timeFrom',
  '$__timeTo',
  '$__unixEpochFilter',
  '$__unixEpochFrom',
  '$__unixEpochTo',
];

export type TrainRewriteWindow = {
  fromMs: number;
  toMs: number;
  visibleFromMs?: number;
  visibleToMs?: number;
  intervalMs: number;
  /**
   * True when `fromMs`/`toMs` came from a lookback resolved against the panel's
   * own now. It rides along into the stored `trainSource` so a cron retrain
   * re-resolves the window instead of replaying a frozen pair.
   */
  relative?: boolean;
  lookbackMs?: number;
};

export type TrainRewriteResult<T> = {
  targets: T[];
  reason?: string;
};

type AnyTarget = Record<string, unknown>;

export function isPostgresType(dsType: string): boolean {
  return dsType === DS_POSTGRES || dsType === DS_POSTGRES_PLUGIN;
}

/** Type-keyed training-query rewrite. Unsupported targets are dropped with a reason. */
export function rewriteTrainTargets<T>(
  dsType: string,
  targets: T[],
  window: TrainRewriteWindow
): TrainRewriteResult<T> {
  const type = dsType || inferDatasourceType(targets[0]);
  if (type === DS_PROMETHEUS) {
    return rewritePrometheus(targets, window);
  }
  if (type === DS_OPENSEARCH) {
    return rewriteOpenSearch(targets, window);
  }
  if (isPostgresType(type)) {
    return rewritePostgres(targets, window);
  }
  return {
    targets: applyLookbackRange(
      targets,
      window.fromMs,
      window.toMs,
      window.visibleFromMs,
      window.visibleToMs
    ),
  };
}

function inferDatasourceType(target: unknown): string {
  const t = asObj(target);
  if (typeof t.expr === 'string' || typeof t.instant === 'boolean' || typeof t.range === 'boolean') {
    return DS_PROMETHEUS;
  }
  if (
    t.queryType === 'PPL' ||
    t.queryType === 'ppl' ||
    t.queryType === 'lucene' ||
    typeof t.luceneQueryType === 'string' ||
    Array.isArray(t.bucketAggs)
  ) {
    return DS_OPENSEARCH;
  }
  if (typeof t.rawSql === 'string') {
    return DS_POSTGRES;
  }
  return '';
}

/** Longest summary `summarizeTrainTargets` returns, prefix included. */
const SUMMARY_LIMIT = 120;

/**
 * One-line identification of the targets a panel trains on. It is stored with the
 * schedule row so the Retrain schedules table can say what a cache hash belongs to;
 * type-keyed like the rewrite, so no datasource field name leaves this module.
 */
export function summarizeTrainTargets(dsType: string, targets: unknown[]): string {
  const type = dsType || inferDatasourceType(targets[0]);
  for (const target of targets) {
    const summary = targetSummary(type, asObj(target));
    if (summary) {
      return clipSummary(summary);
    }
  }
  return '';
}

function targetSummary(type: string, t: AnyTarget): string {
  if (type === DS_PROMETHEUS) {
    return prefixed('PromQL: ', t.expr);
  }
  if (type === DS_OPENSEARCH) {
    const ppl = String(t.queryType ?? '').toLowerCase() === 'ppl';
    return prefixed(ppl ? 'PPL: ' : 'Lucene: ', t.query);
  }
  if (isPostgresType(type)) {
    return prefixed('SQL: ', t.rawSql);
  }
  if (type === DS_DRUID) {
    const builder = asObj(t.builder);
    const sql = prefixed('Druid SQL: ', builder.query);
    if (sql) {
      return sql;
    }
    // The Druid builder stores no query text: name the table it reads, and its rollup.
    const table = asObj(builder.dataSource).name;
    if (typeof table !== 'string' || !table) {
      return '';
    }
    const granularity = typeof builder.granularity === 'string' && builder.granularity ? ` · ${builder.granularity}` : '';
    return `Druid: ${table}${granularity}`;
  }
  return '';
}

function prefixed(prefix: string, value: unknown): string {
  return typeof value === 'string' && value.trim() ? prefix + value : '';
}

function clipSummary(summary: string): string {
  // Whitespace collapses first: a multi-line SQL target would otherwise store its
  // newlines in the row and wrap the table cell onto a dozen lines.
  const flat = summary.replace(/\s+/g, ' ').trim();
  return flat.length > SUMMARY_LIMIT ? `${flat.slice(0, SUMMARY_LIMIT - 1)}…` : flat;
}

function rewritePrometheus<T>(targets: T[], window: TrainRewriteWindow): TrainRewriteResult<T> {
  const step = trainStepInterval(window.intervalMs);
  const kept: T[] = [];
  let reason: string | undefined;
  for (const target of cloneTargets(targets)) {
    const t = asObj(target);
    if (isPrometheusInstant(t)) {
      reason = REASON_UNSUPPORTED_PROM_INSTANT;
      continue;
    }
    t.range = true;
    t.instant = false;
    t.exemplar = false;
    t.interval = step;
    kept.push(target);
  }
  return { targets: kept, reason: kept.length === 0 ? reason : undefined };
}

function isPrometheusInstant(t: AnyTarget): boolean {
  return t.instant === true && t.range !== true;
}

function rewriteOpenSearch<T>(targets: T[], window: TrainRewriteWindow): TrainRewriteResult<T> {
  const step = trainStepInterval(window.intervalMs);
  const kept: T[] = [];
  let reason: string | undefined;
  for (const target of cloneTargets(targets)) {
    const t = asObj(target);
    if (isOpenSearchUnsupported(t)) {
      reason = REASON_UNSUPPORTED_OS_QUERY;
      continue;
    }
    const aggs = t.bucketAggs;
    if (Array.isArray(aggs)) {
      t.bucketAggs = aggs.map((agg) => pinDateHistogram(asObj(agg), step));
    }
    kept.push(target);
  }
  return { targets: kept, reason: kept.length === 0 ? reason : undefined };
}

function isOpenSearchUnsupported(t: AnyTarget): boolean {
  const queryType = String(t.queryType ?? 'lucene').toLowerCase();
  if (queryType === 'ppl') {
    return t.format !== 'time_series';
  }
  const lucene = String(t.luceneQueryType ?? '');
  if (
    lucene === 'Logs' ||
    lucene === 'RawData' ||
    lucene === 'RawDocument' ||
    lucene === 'Traces' ||
    lucene === 'TracesList'
  ) {
    return true;
  }
  if (t.isLogsQuery === true && lucene !== 'Metric' && lucene !== 'Metrics') {
    return true;
  }
  return false;
}

function pinDateHistogram(agg: AnyTarget, step: string): AnyTarget {
  if (agg.type !== 'date_histogram') {
    return agg;
  }
  const settings = asObj(agg.settings);
  return { ...agg, settings: { ...settings, interval: step } };
}

function rewritePostgres<T>(targets: T[], window: TrainRewriteWindow): TrainRewriteResult<T> {
  const kept: T[] = [];
  let reason: string | undefined;
  for (const target of cloneTargets(targets)) {
    const t = asObj(target);
    if (isPostgresUnsupported(t)) {
      reason = REASON_UNSUPPORTED_PG_FORMAT;
      continue;
    }
    if (t.format == null) {
      t.format = 'time_series';
    }
    if (typeof t.rawSql === 'string') {
      t.rawSql = rewritePostgresSql(t.rawSql, window);
    }
    kept.push(target);
  }
  return { targets: kept, reason: kept.length === 0 ? reason : undefined };
}

function isPostgresUnsupported(t: AnyTarget): boolean {
  const format = t.format;
  if (format === 'table' || format === 1 || format === 'logs' || format === 'explain') {
    return true;
  }
  if (typeof t.rawSql === 'string' && /^\s*EXPLAIN\b/i.test(t.rawSql)) {
    return true;
  }
  return false;
}

export function rewritePostgresSql(sql: string, window: TrainRewriteWindow): string {
  if (PG_TIME_MACROS.some((macro) => sql.includes(macro))) {
    return sql;
  }
  let out = sql;
  out = replaceVisibleIso(out, window.visibleFromMs, window.fromMs);
  out = replaceVisibleIso(out, window.visibleToMs, window.toMs);
  return out;
}

function replaceVisibleIso(sql: string, visibleMs: number | undefined, trainMs: number): string {
  if (visibleMs == null || visibleMs === trainMs) {
    return sql;
  }
  let out = replaceAll(sql, isoUtc(visibleMs), isoUtc(trainMs));
  out = replaceAll(out, rfc3339Utc(visibleMs), rfc3339Utc(trainMs));
  return out;
}

function cloneTargets<T>(targets: T[]): T[] {
  return JSON.parse(JSON.stringify(targets)) as T[];
}

function asObj(value: unknown): AnyTarget {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as AnyTarget;
  }
  return {};
}

function replaceAll(haystack: string, needle: string, replacement: string): string {
  if (!needle) {
    return haystack;
  }
  return haystack.split(needle).join(replacement);
}
