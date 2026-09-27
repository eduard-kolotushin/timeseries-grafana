import { DataQuery } from '@grafana/data';
import { cacheKey, CacheKeyInput } from '../forecast-panel/cacheKey';
import { isForecastTarget } from '../forecast-panel/mixed';
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

export function siblingMetricQueries(
  queries: Array<DataQuery & { hide?: boolean }> | undefined,
  selfRefId?: string
): DataQuery[] {
  return (queries ?? []).filter((q) => q.refId !== selfRefId && !q.hide && !isForecastTarget(q));
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

/**
 * Replace sourceTargets with the given inner queries, all under one datasource. An array
 * (not a single element) because the overlay fingerprints every visible metric target, so
 * a two-target panel's key is only reachable when this holds both.
 */
export function withSourceTarget(
  query: ForecastDataQuery,
  ds: DataSourceRef,
  inners: Array<Record<string, unknown>>
): ForecastDataQuery {
  return {
    ...query,
    sourceTargets: inners.map((inner) => ({ ...inner, datasource: { uid: ds.uid, type: ds.type } })),
  };
}

/**
 * Copy the panel's metric siblings into sourceTargets. The overlay fingerprints every
 * visible metric target, so the alerting query's key can only match when this holds them
 * all in the same order, each under its own datasource. Does not copy model, train range,
 * or series name.
 */
export function copySourceFromSibling(query: ForecastDataQuery, siblings: DataQuery[]): ForecastDataQuery {
  return {
    ...query,
    sourceTargets: siblings.map((sibling) => ({
      ...(sibling as unknown as Record<string, unknown>),
      datasource:
        typeof sibling.datasource === 'string'
          ? { uid: sibling.datasource }
          : { uid: sibling.datasource?.uid, type: sibling.datasource?.type },
    })),
  };
}
