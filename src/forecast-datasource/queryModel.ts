import { DataQuery } from '@grafana/data';
import { cacheKey, CacheKeyInput } from '../forecast-panel/cacheKey';
import { ForecastOptions } from '../forecast-panel/types';
import { defaultForecastQuery, ForecastDataQuery } from './types';

export function optionsFromQuery(query: ForecastDataQuery): ForecastOptions {
  return {
    model: query.model ?? defaultForecastQuery.model,
    alpha: query.alpha ?? defaultForecastQuery.alpha,
    beta: query.beta ?? defaultForecastQuery.beta,
    period: query.period ?? defaultForecastQuery.period,
    season: query.season ?? defaultForecastQuery.season,
    calendar: query.calendar ?? defaultForecastQuery.calendar,
    showInterval: true,
    interval: query.level ?? defaultForecastQuery.level,
    trainRange: query.trainRange ?? { from: '', to: '' },
    forecastRange: { from: '', to: '' },
    lookback: query.lookback,
  };
}

export function cacheKeyInputFromQuery(query: ForecastDataQuery): CacheKeyInput {
  return {
    targets: Array.isArray(query.sourceTargets) ? query.sourceTargets : [],
    options: optionsFromQuery(query),
    seriesName: query.seriesName ?? '',
  };
}

export async function withCacheKey(query: ForecastDataQuery): Promise<ForecastDataQuery> {
  const key = await cacheKey(cacheKeyInputFromQuery(query));
  return { ...query, cacheKey: key };
}

export type DataSourceRef = { uid?: string; type?: string };

export function sourceDatasource(query: ForecastDataQuery): DataSourceRef | undefined {
  const targets = Array.isArray(query.sourceTargets) ? query.sourceTargets : [];
  const first = targets[0];
  if (!first || typeof first !== 'object') {
    return undefined;
  }
  const ds = (first as { datasource?: DataSourceRef }).datasource;
  if (!ds?.uid) {
    return undefined;
  }
  return ds;
}

export function innerSourceQuery(query: ForecastDataQuery): DataQuery {
  const targets = Array.isArray(query.sourceTargets) ? query.sourceTargets : [];
  const first = targets[0];
  if (!first || typeof first !== 'object') {
    return { refId: 'A' };
  }
  const inner = first as Record<string, unknown>;
  const refId = typeof inner.refId === 'string' ? inner.refId : 'A';
  return { ...inner, refId } as DataQuery;
}

export function withSourceTarget(
  query: ForecastDataQuery,
  ds: DataSourceRef,
  inner: Record<string, unknown>
): ForecastDataQuery {
  return {
    ...query,
    sourceTargets: [{ ...inner, datasource: { uid: ds.uid, type: ds.type } }],
  };
}
