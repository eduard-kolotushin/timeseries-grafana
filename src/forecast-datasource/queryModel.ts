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

/**
 * A Forecast query synthesized from overlay options, the inverse of `optionsFromQuery`.
 * One alert rule is built from a panel's fit: the metric targets it was fitted on become
 * `sourceTargets` (so the panel's own cacheKey is reproducible), the panel's fit options
 * become the model, and the series the rule is for names the answer — and labels it, so a
 * Math node pairs the baseline with that series instead of broadcasting to all of them.
 * `datasource` is the Forecast datasource instance the rule will read through.
 */
export function queryFromOptions(input: {
  refId: string;
  options?: ForecastOptions;
  sourceTargets: unknown[];
  seriesName: string;
  seriesLabels?: Record<string, string>;
  datasource: { uid: string; type?: string };
}): ForecastDataQuery {
  const options = input.options;
  const query: ForecastDataQuery = {
    ...defaultForecastQuery,
    refId: input.refId,
    seriesName: input.seriesName,
    datasource: input.datasource,
    model: options?.model ?? defaultForecastQuery.model,
    alpha: options?.alpha ?? defaultForecastQuery.alpha,
    beta: options?.beta ?? defaultForecastQuery.beta,
    period: options?.period ?? defaultForecastQuery.period,
    season: options?.season ?? defaultForecastQuery.season,
    calendar: options?.calendar ?? defaultForecastQuery.calendar,
    level: options?.interval ?? defaultForecastQuery.level,
    trainRange: options?.trainRange ?? defaultForecastQuery.trainRange,
    sourceTargets: input.sourceTargets,
  };
  if (input.seriesLabels && Object.keys(input.seriesLabels).length > 0) {
    query.seriesLabels = input.seriesLabels;
  }
  return query;
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
