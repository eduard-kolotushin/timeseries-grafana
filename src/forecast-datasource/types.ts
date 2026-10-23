import { DataQuery, DataSourceJsonData } from '@grafana/data';
import { BaselineCalendar, BaselineSeason, ForecastModel, TrainTimeRange } from '../forecast-panel/types';

export const FORECAST_DATASOURCE_TYPE = 'eduardkolotushin-forecast-datasource';

export type ForecastOutputKind = 'forecast' | 'lower' | 'upper';

export interface ForecastDataQuery extends DataQuery {
  kind: ForecastOutputKind;
  seriesName: string;
  model: ForecastModel;
  alpha: number;
  beta: number;
  period: number;
  season: BaselineSeason;
  calendar: BaselineCalendar;
  /** Prediction-interval coverage in (0, 1). Used when kind is lower or upper. */
  level: number;
  trainRange: TrainTimeRange;
  /** Copy of overlay panel targets (query A) for the cacheKey fingerprint. */
  sourceTargets: unknown[];
  /**
   * Labels the restored frame's Value field with. A rule that also queries the metric
   * pairs this baseline with the matching series by label instead of broadcasting to
   * every series the metric query returned. Never part of the cacheKey fingerprint.
   */
  seriesLabels?: Record<string, string>;
  cacheKey?: string;
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
  seriesName: '',
  model: 'holt',
  alpha: 0.8,
  beta: 0.2,
  period: 7,
  season: 'hour',
  calendar: '',
  level: 0.95,
  trainRange: { from: '', to: '' },
  sourceTargets: [],
};
