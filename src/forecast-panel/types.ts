export type ForecastModel = 'naive' | 'mean' | 'drift' | 'seasonal' | 'ses' | 'holt';

export interface ForecastOptions {
  model: ForecastModel;
  horizon: number;
  alpha: number;
  beta: number;
  period: number;
}

export interface ForecastResponse {
  times: number[];
  values: Array<number | null>;
}
