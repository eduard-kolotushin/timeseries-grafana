import { DataQuery, DataSourceJsonData } from '@grafana/data';

export const FORECAST_DATASOURCE_TYPE = 'eduardkolotushin-forecast-datasource';

/** The key format the backend accepts (`cacheKeyPattern` in pkg/plugin/forecast.go). */
export const CACHE_KEY_PATTERN = /^[a-f0-9]{64}$/;

export type ForecastOutputKind = 'forecast' | 'lower' | 'upper';

/**
 * A Forecast datasource query restores one trained baseline. `cacheKey` is the whole
 * identity: the overlay's fit wrote the snapshot under it, and the model and training
 * window are inside the snapshot, so querying needs nothing else. The panel options
 * list a trained key per series; New alert rule fills it in automatically.
 */
export interface ForecastDataQuery extends DataQuery {
  kind: ForecastOutputKind;
  /** The trained baseline's 64-char lowercase-hex cache key. */
  cacheKey?: string;
  /** Prediction-interval coverage in (0, 1). Used when kind is lower or upper. */
  level: number;
  /**
   * Labels the restored frame's Value field with. A rule that also queries the metric
   * pairs this baseline with the matching series by label instead of broadcasting to
   * every series the metric query returned. Never part of the cacheKey.
   */
  seriesLabels?: Record<string, string>;
}

export interface ForecastDataSourceOptions extends DataSourceJsonData {
  storeHost?: string;
  storePort?: number | string;
  storeDatabase?: string;
  storeUser?: string;
  storeSslMode?: string;
}

export interface ForecastSecureJsonData {
  storePassword?: string;
}

export const defaultForecastQuery: Omit<ForecastDataQuery, 'refId'> = {
  kind: 'forecast',
  cacheKey: '',
  level: 0.95,
};
