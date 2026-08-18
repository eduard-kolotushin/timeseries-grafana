export type ForecastModel = 'naive' | 'mean' | 'drift' | 'seasonal' | 'baseline' | 'ses' | 'holt';
export type BaselineSeason = 'hour' | 'day' | 'week' | 'minute-week';
export type BaselineCalendar = '' | 'ru';

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
  lookback: string;
}

export interface ForecastResponse {
  times: number[];
  values: Array<number | null>;
  lower?: Array<number | null>;
  upper?: Array<number | null>;
}
