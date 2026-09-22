import { dateTime, dateTimeFormat, dateTimeParse, getTimeZone, rangeUtil } from '@grafana/data';
import { BaselineSeason, ForecastModel, TrainTimeRange } from './types';

export const MAX_TRAIN_POINTS = 100_000;

const MINUTE_MS = 60_000;
const HOUR_MS = 3_600_000;
const DAY_MS = 86_400_000;

/** Model-aware training step. Floor 1m unless the seasonal baseline pins 1h/1d. */
export function trainStepMs(model: ForecastModel, season: BaselineSeason | undefined, intervalMs: number): number {
  if (model === 'baseline') {
    if (season === 'minute-week') {
      return MINUTE_MS;
    }
    if (season === 'hour' || season === 'week') {
      return HOUR_MS;
    }
    if (season === 'day') {
      return DAY_MS;
    }
  }
  const step = intervalMs > 0 ? intervalMs : MINUTE_MS;
  return Math.max(step, MINUTE_MS);
}

/** Grafana interval string for a millisecond step (`1m`, `1h`, `1d`). */
export function trainStepInterval(ms: number): string {
  const step = ms > 0 ? ms : MINUTE_MS;
  if (step % DAY_MS === 0) {
    return `${step / DAY_MS}d`;
  }
  if (step % HOUR_MS === 0) {
    return `${step / HOUR_MS}h`;
  }
  if (step % MINUTE_MS === 0) {
    return `${step / MINUTE_MS}m`;
  }
  if (step % 1000 === 0) {
    return `${step / 1000}s`;
  }
  return `${step}ms`;
}

export function autoLookback(model: ForecastModel, season?: BaselineSeason): string {
  switch (model) {
    case 'baseline':
      if (season === 'minute-week' || season === 'week') {
        return '21d';
      }
      if (season === 'day') {
        return '56d';
      }
      return '14d';
    case 'seasonal':
      return '14d';
    default:
      return '7d';
  }
}

/** Auto forecast window length starting at Grafana dashboard `now`. */
export function autoForecastHorizon(model: ForecastModel, season?: BaselineSeason): string {
  switch (model) {
    case 'baseline':
      if (season === 'minute-week' || season === 'week') {
        return '6h';
      }
      if (season === 'day') {
        return '7d';
      }
      return '24h';
    case 'seasonal':
      return '24h';
    default:
      return '6h';
  }
}

/**
 * Dashboard timezone (`utc`, `browser`, or IANA) from a Grafana scene graph.
 * Prefers SceneTimeRange.getTimeZone() so Default/`browser` matches PanelProps.timeZone.
 */
export function timeZoneFromScene(root: unknown, depth = 0): string | undefined {
  if (!root || typeof root !== 'object' || depth > 8) {
    return undefined;
  }
  const obj = root as {
    parent?: unknown;
    getTimeZone?: () => unknown;
    state?: { timeZone?: unknown; $timeRange?: unknown };
  };
  if (typeof obj.getTimeZone === 'function') {
    const tz = obj.getTimeZone();
    if (typeof tz === 'string' && tz.trim()) {
      return tz.trim();
    }
  }
  if (typeof obj.state?.timeZone === 'string' && obj.state.timeZone.trim()) {
    return obj.state.timeZone.trim();
  }
  const nested = timeZoneFromScene(obj.state?.$timeRange, depth + 1);
  if (nested) {
    return nested;
  }
  return timeZoneFromScene(obj.parent, depth + 1);
}

/**
 * Same timezone the overlay panel uses. The mounted dashboard scene is the panel's own
 * source for `PanelProps.timeZone`, so it wins; with no scene mounted, Grafana's own
 * resolver answers with the user's configured zone — what a dashboard left on Default
 * resolves to for the panel — and only then Grafana's `browser` default. The options
 * editor receives no `PanelProps`, so this chain is as close to the panel as the API
 * allows (a `StandardEditorProps` context carries frames, options and an event bus, no
 * timezone).
 */
export function dashboardTimeZone(): string {
  const scene =
    typeof window === 'undefined'
      ? undefined
      : timeZoneFromScene((window as Window & { __grafanaSceneContext?: unknown }).__grafanaSceneContext);
  return getTimeZone({ timeZone: scene ?? '' });
}

export type CivilDate = { year: number; month: number; day: number };

function pad2(n: number): string {
  return n.toString().padStart(2, '0');
}

/** Calendar Y-M-D of an instant in a Grafana timezone. month is 1-based. */
export function civilYmd(ms: number, timeZone: string): CivilDate {
  const s = dateTimeFormat(ms, { timeZone, format: 'YYYY-MM-DD' });
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s);
  if (!m) {
    const d = new Date(ms);
    return { year: d.getUTCFullYear(), month: d.getUTCMonth() + 1, day: d.getUTCDate() };
  }
  return { year: Number(m[1]), month: Number(m[2]), day: Number(m[3]) };
}

/**
 * Absolute picker string for a civil day in `timeZone` (Grafana dashboard TZ).
 * Stored without a Z suffix, same as the dashboard time picker; parse with the same zone.
 */
export function absoluteDayBound(date: CivilDate, timeZone: string, endOfDay: boolean): string {
  const raw = `${date.year}-${pad2(date.month)}-${pad2(date.day)} ${endOfDay ? '23:59:59' : '00:00:00'}`;
  try {
    const dt = dateTimeParse(raw, { timeZone });
    if (dt.isValid()) {
      return dateTimeFormat(dt, { timeZone, format: 'YYYY-MM-DD HH:mm:ss' });
    }
  } catch {
    // keep raw
  }
  return raw;
}

/** Grafana time-picker `now` (honors nowDelay), not wall clock and not panel `to`. */
export function dashboardNowMs(timeZone?: string): number {
  try {
    const parsed = rangeUtil.convertRawToRange({ from: 'now', to: 'now' }, timeZone);
    const ms = parsed.from.valueOf();
    if (Number.isFinite(ms)) {
      return ms;
    }
  } catch {
    // fall through
  }
  return dateTime().valueOf();
}

export function resolveLookbackMs(options: {
  model: ForecastModel;
  season?: BaselineSeason;
  lookback?: string;
}): number {
  const override = options.lookback?.trim();
  if (override && override.toLowerCase() !== 'auto') {
    const duration = /^\d+(\.\d+)?$/.test(override) ? `${override}d` : override;
    try {
      const ms = rangeUtil.intervalToMs(duration);
      if (ms > 0) {
        return ms;
      }
    } catch {
      // fall through to Auto
    }
  }
  return rangeUtil.intervalToMs(autoLookback(options.model, options.season));
}

export function isExplicitAutoTrainRange(range?: TrainTimeRange): boolean {
  if (range == null) {
    return false;
  }
  const from = range.from?.trim() ?? '';
  const to = range.to?.trim() ?? '';
  return !from || !to || (from.toLowerCase() === 'auto' && to.toLowerCase() === 'auto');
}

/**
 * A Grafana relative bound, the shape the picker's Quick ranges write (`now`, `now-7d`,
 * `now+6h`). Only `[now - lookbackMs, now]` is expressible as a relative window, so a
 * bound pair counts as one only when `to` is anchored at `now`; a window that does not
 * end there, a rounded bound (`now/d`, whose width the stored lookback cannot replay),
 * or a calendar or hand-typed absolute bound stays absolute, which is faithful for it.
 */
const RELATIVE_BOUND = /^now(?:[+-]\d+(?:\.\d+)?(?:ms|s|m|h|d|w|M|y))*$/;

function parseTimeRange(
  range: TrainTimeRange,
  timeZone?: string,
  allowEqual = false
): { fromMs: number; toMs: number } | null {
  try {
    const parsed = rangeUtil.convertRawToRange({ from: range.from, to: range.to }, timeZone);
    const fromMs = parsed.from.valueOf();
    const toMs = parsed.to.valueOf();
    if (Number.isFinite(fromMs) && Number.isFinite(toMs) && (allowEqual ? toMs >= fromMs : toMs > fromMs)) {
      return { fromMs, toMs };
    }
  } catch {
    return null;
  }
  return null;
}

/**
 * Training from/to window plus how it was resolved. `relative` is true when the
 * window came from a lookback against the panel's own now, which is what a cron
 * retrain must re-resolve instead of replaying `fromMs`/`toMs` verbatim.
 */
/** A training window resolved from the picker or from the Auto lookback. */
export type ResolvedTrainWindow = {
  fromMs: number;
  toMs: number;
  relative: boolean;
  /** Width to re-resolve at claim time. 0 when the window is absolute. */
  lookbackMs: number;
};

export type TrainWindow = ResolvedTrainWindow | { invalid: true };

export function isInvalidTrainWindow(w: TrainWindow): w is { invalid: true } {
  return 'invalid' in w;
}

/** Training from/to window. Explicit empty picker is Auto (ignores legacy `lookback`). */
export function resolveTrainWindow(
  options: {
    model: ForecastModel;
    season?: BaselineSeason;
    lookback?: string;
    trainRange?: TrainTimeRange;
  },
  panelToMs: number,
  timeZone?: string
): TrainWindow {
  if (options.trainRange != null && !isExplicitAutoTrainRange(options.trainRange)) {
    const parsed = parseTimeRange(options.trainRange, timeZone);
    if (parsed) {
      const rawFrom = options.trainRange.from?.trim() ?? '';
      if (RELATIVE_BOUND.test(rawFrom) && (options.trainRange.to?.trim() ?? '') === 'now') {
        // The Quick ranges are relative strings ("Last 7 days" is `now-7d` from `now`): a
        // cron retrain must re-resolve that lookback, or one frozen week is replayed forever.
        return { ...parsed, relative: true, lookbackMs: parsed.toMs - parsed.fromMs };
      }
      // An absolute picker means those dates, not "the last N hours": replaying
      // them verbatim is the only faithful retrain.
      return { ...parsed, relative: false, lookbackMs: 0 };
    }
    // An explicit picker that will not parse is not Auto: the two windows look alike in
    // the editor, so both report the same way (see resolveForecastWindow).
    return { invalid: true };
  }
  const lookbackMs =
    options.trainRange != null && isExplicitAutoTrainRange(options.trainRange)
      ? rangeUtil.intervalToMs(autoLookback(options.model, options.season))
      : resolveLookbackMs(options);
  return { fromMs: panelToMs - lookbackMs, toMs: panelToMs, relative: true, lookbackMs };
}

export type ForecastWindow = { fromMs: number; toMs: number } | { invalid: true };

export function isInvalidForecastWindow(w: ForecastWindow): w is { invalid: true } {
  return 'invalid' in w;
}

/** Forecast from/to window. Empty picker is Auto (`now` → `now` + model duration). Invalid explicit range is not Auto. */
export function resolveForecastWindow(
  options: {
    model: ForecastModel;
    season?: BaselineSeason;
    forecastRange?: TrainTimeRange;
  },
  nowMs: number,
  timeZone?: string
): ForecastWindow {
  if (options.forecastRange != null && !isExplicitAutoTrainRange(options.forecastRange)) {
    const parsed = parseTimeRange(options.forecastRange, timeZone, true);
    if (parsed) {
      return parsed;
    }
    return { invalid: true };
  }
  const durationMs = rangeUtil.intervalToMs(autoForecastHorizon(options.model, options.season));
  return { fromMs: nowMs, toMs: nowMs + durationMs };
}

export function trainMaxDataPoints(lookbackMs: number, intervalMs: number): number {
  const step = intervalMs > 0 ? intervalMs : 60_000;
  return Math.min(MAX_TRAIN_POINTS, Math.ceil(lookbackMs / step) + 1);
}

export function forecastLevel(options: { showInterval?: boolean; interval?: number }): number {
  if (options.showInterval === false) {
    return 0;
  }
  const n = options.interval ?? 0.95;
  return n > 0 ? n : 0;
}

export function isoUtc(ms: number): string {
  return new Date(ms).toISOString();
}

/** RFC3339 UTC without milliseconds (`…05Z`), as Grafana SQL macros often expand. */
export function rfc3339Utc(ms: number): string {
  return isoUtc(ms).replace(/\.\d{3}Z$/, 'Z');
}

/** Clone targets and pin Druid/SQL time bounds to the training window. */
export function applyLookbackRange<T>(
  targets: T[],
  fromMs: number,
  toMs: number,
  visibleFromMs?: number,
  visibleToMs?: number
): T[] {
  const fromIso = isoUtc(fromMs);
  const toIso = isoUtc(toMs);
  const rewrite = (text: string): string => {
    let out = replaceAll(text, '${__from:date:iso}', fromIso);
    out = replaceAll(out, '${__to:date:iso}', toIso);
    out = replaceAll(out, '${__from}', String(fromMs));
    out = replaceAll(out, '${__to}', String(toMs));
    if (visibleFromMs != null && visibleToMs != null && visibleFromMs !== fromMs) {
      out = replaceAll(out, isoUtc(visibleFromMs), fromIso);
      out = replaceAll(out, String(visibleFromMs), String(fromMs));
    }
    if (visibleFromMs != null && visibleToMs != null && visibleToMs !== toMs) {
      out = replaceAll(out, isoUtc(visibleToMs), toIso);
      out = replaceAll(out, String(visibleToMs), String(toMs));
    }
    return out;
  };
  // Literals are replaced in the target's string leaves, not in its serialized JSON text:
  // a text replace also hit object keys and the digits of unrelated strings.
  const pinned = new Map<number, number>();
  if (visibleFromMs != null && visibleFromMs !== fromMs) {
    pinned.set(visibleFromMs, fromMs);
  }
  if (visibleToMs != null && visibleToMs !== toMs) {
    pinned.set(visibleToMs, toMs);
  }
  const cloned = JSON.parse(JSON.stringify(targets)) as T[];
  for (const target of cloned) {
    rewriteLeaves(target, rewrite, pinned);
  }
  for (const target of cloned) {
    if (isDruidIntervalsTarget(target)) {
      target.builder.intervals.intervals = target.builder.intervals.intervals.map(() => `${fromIso}/${toIso}`);
    }
  }
  return cloned;
}

type DruidIntervalsTarget = { builder: { intervals: { intervals: string[] } } };

/** A Druid builder target whose interval list this module pins to the training window. */
function isDruidIntervalsTarget(target: unknown): target is DruidIntervalsTarget {
  if (target === null || typeof target !== 'object' || !('builder' in target)) {
    return false;
  }
  const { builder } = target;
  if (builder === null || typeof builder !== 'object' || !('intervals' in builder)) {
    return false;
  }
  const { intervals } = builder;
  return (
    intervals !== null &&
    typeof intervals === 'object' &&
    'intervals' in intervals &&
    Array.isArray(intervals.intervals)
  );
}

/** Rewrite every string leaf under `node` and every number leaf pinning a visible bound. */
function rewriteLeaves(node: unknown, rewrite: (text: string) => string, pinned: Map<number, number>): void {
  if (Array.isArray(node)) {
    for (let i = 0; i < node.length; i++) {
      node[i] = rewriteLeaf(node[i], rewrite, pinned);
    }
    return;
  }
  if (node === null || typeof node !== 'object') {
    return;
  }
  // The clone came out of `JSON.parse`, so it holds plain objects: string-keyed indexing is all this needs.
  const record = node as Record<string, unknown>;
  for (const key of Object.keys(record)) {
    record[key] = rewriteLeaf(record[key], rewrite, pinned);
  }
}

function rewriteLeaf(value: unknown, rewrite: (text: string) => string, pinned: Map<number, number>): unknown {
  if (typeof value === 'string') {
    return rewrite(value);
  }
  if (typeof value === 'number') {
    return pinned.get(value) ?? value;
  }
  rewriteLeaves(value, rewrite, pinned);
  return value;
}

function replaceAll(haystack: string, needle: string, replacement: string): string {
  if (!needle) {
    return haystack;
  }
  return haystack.split(needle).join(replacement);
}
