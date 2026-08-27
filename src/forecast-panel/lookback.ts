import { dateTime, rangeUtil } from '@grafana/data';
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
): { fromMs: number; toMs: number } {
  if (options.trainRange != null && !isExplicitAutoTrainRange(options.trainRange)) {
    const parsed = parseTimeRange(options.trainRange, timeZone);
    if (parsed) {
      return parsed;
    }
  }
  const lookbackMs =
    options.trainRange != null && isExplicitAutoTrainRange(options.trainRange)
      ? rangeUtil.intervalToMs(autoLookback(options.model, options.season))
      : resolveLookbackMs(options);
  return { fromMs: panelToMs - lookbackMs, toMs: panelToMs };
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
  let json = JSON.stringify(targets);
  json = replaceAll(json, '${__from:date:iso}', fromIso);
  json = replaceAll(json, '${__to:date:iso}', toIso);
  json = replaceAll(json, '${__from}', String(fromMs));
  json = replaceAll(json, '${__to}', String(toMs));
  if (visibleFromMs != null && visibleToMs != null && visibleFromMs !== fromMs) {
    json = replaceAll(json, isoUtc(visibleFromMs), fromIso);
    json = replaceAll(json, String(visibleFromMs), String(fromMs));
  }
  if (visibleFromMs != null && visibleToMs != null && visibleToMs !== toMs) {
    json = replaceAll(json, isoUtc(visibleToMs), toIso);
    json = replaceAll(json, String(visibleToMs), String(toMs));
  }
  const cloned = JSON.parse(json) as T[];
  for (const target of cloned) {
    const builder = (target as { builder?: { intervals?: { intervals?: string[] } } }).builder;
    if (Array.isArray(builder?.intervals?.intervals)) {
      builder.intervals.intervals = builder.intervals.intervals.map(() => `${fromIso}/${toIso}`);
    }
  }
  return cloned;
}

function replaceAll(haystack: string, needle: string, replacement: string): string {
  if (!needle) {
    return haystack;
  }
  return haystack.split(needle).join(replacement);
}
