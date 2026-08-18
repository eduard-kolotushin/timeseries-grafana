import { rangeUtil } from '@grafana/data';
import { BaselineSeason, ForecastModel } from './types';

export const MAX_TRAIN_POINTS = 100_000;

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
