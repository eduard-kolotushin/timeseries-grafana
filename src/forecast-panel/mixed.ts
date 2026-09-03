import { DataFrame, DataQuery } from '@grafana/data';
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
