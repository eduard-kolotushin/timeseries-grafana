export type ForecastModel = 'naive' | 'mean' | 'drift' | 'seasonal' | 'baseline' | 'ses' | 'holt';
export type BaselineSeason = 'hour' | 'day' | 'week' | 'minute-week';
export type BaselineCalendar = '' | 'ru';

/** Grafana raw from/to, same strings as the dashboard time picker. Empty is Auto. */
export interface TrainTimeRange {
  from: string;
  to: string;
}

export interface ForecastOptions {
  model: ForecastModel;
  horizon: number;
  alpha: number;
  beta: number;
  period: number;
  season: BaselineSeason;
  calendar: BaselineCalendar;
  showInterval: boolean;
  interval: number;
  trainRange: TrainTimeRange;
  /** @deprecated Duration lookback (`15d`); used only when `trainRange` was never saved. */
  lookback?: string;
}

export interface ForecastResponse {
  times: number[];
  values: Array<number | null>;
  lower?: Array<number | null>;
  upper?: Array<number | null>;
}
