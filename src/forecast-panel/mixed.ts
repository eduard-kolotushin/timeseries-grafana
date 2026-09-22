import { DataFrame, DataQuery, DateTime } from '@grafana/data';
import { FORECAST_DATASOURCE_TYPE } from '../forecast-datasource/types';

export function datasourceType(ref: DataQuery['datasource'] | string | null | undefined): string {
  if (!ref) {
    return '';
  }
  if (typeof ref === 'string') {
    return ref;
  }
  return ref.type ?? '';
}

export function isForecastTarget(q: {
  refId?: string;
  datasource?: DataQuery['datasource'];
  kind?: unknown;
  sourceTargets?: unknown;
  cacheKey?: unknown;
}): boolean {
  if (datasourceType(q.datasource) === FORECAST_DATASOURCE_TYPE) {
    return true;
  }
  if (q.kind === 'forecast' || q.kind === 'lower' || q.kind === 'upper') {
    return Array.isArray(q.sourceTargets) || typeof q.cacheKey === 'string';
  }
  return false;
}

export function metricTargets<T extends { hide?: boolean; datasource?: DataQuery['datasource'] }>(targets: T[]): T[] {
  return targets.filter((t) => !t.hide && !isForecastTarget(t));
}

export function splitPanelFrames(
  series: DataFrame[],
  targets: Array<{ refId?: string; datasource?: DataQuery['datasource'] }>
): { history: DataFrame[]; datasource: DataFrame[] } {
  const forecastIds = new Set(
    targets.filter(isForecastTarget).map((t) => t.refId).filter((id): id is string => Boolean(id))
  );
  const history: DataFrame[] = [];
  const datasource: DataFrame[] = [];
  for (const frame of series) {
    const id = frame.refId || frame.name;
    if (id && forecastIds.has(id)) {
      datasource.push(frame);
    } else {
      history.push(frame);
    }
  }
  return { history, datasource };
}

/** The parts of a panel request that decide which frames belong to it. */
export type FrameQueryIdentity = {
  range?: { from?: DateTime | number; to?: DateTime | number };
  targets?: Array<{ refId?: string }>;
  intervalMs?: number;
};

/**
 * Identity of the query the panel's frames answer: its range, refIds and resolution. A
 * re-render of the same query yields the same key, so only a real query change moves it.
 */
export function framesQueryKey(request: FrameQueryIdentity | undefined): string {
  if (!request) {
    return '';
  }
  const refs = (request.targets ?? []).map((target) => target.refId ?? '').join(',');
  return `${epochMs(request.range?.from)}|${epochMs(request.range?.to)}|${refs}|${request.intervalMs ?? 0}`;
}

function epochMs(value?: DateTime | number): number {
  const ms = typeof value === 'number' ? value : value?.valueOf();
  return typeof ms === 'number' && Number.isFinite(ms) ? ms : 0;
}

/** A completed load: the query it answered plus the frames it drew. */
export type LoadedFrames = { key: string; frames: DataFrame[] };

/**
 * The frames to draw: the completed load only while it belongs to the panel's current
 * query, the current history otherwise. A superseded load leaves no result behind, so
 * the previous range's frames are never drawn once the query changed; when nothing
 * changed the completed load keeps being drawn, so the forecast does not flicker.
 */
export function drawableFrames(load: LoadedFrames | undefined, queryKey: string, history: DataFrame[]): DataFrame[] {
  if (load == null || load.key !== queryKey || load.frames.length === 0) {
    return history;
  }
  return load.frames;
}
