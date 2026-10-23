export type ForecastModel = 'naive' | 'mean' | 'drift' | 'seasonal' | 'baseline' | 'ses' | 'holt';
export type BaselineSeason = 'hour' | 'day' | 'week' | 'minute-week';
export type BaselineCalendar = '' | 'ru';

/** Grafana raw from/to, same strings as the dashboard time picker (dashboard timezone). Empty is Auto. */
export interface TrainTimeRange {
  from: string;
  to: string;
}

export interface ForecastOptions {
  model: ForecastModel;
  alpha: number;
  beta: number;
  period: number;
  season: BaselineSeason;
  calendar: BaselineCalendar;
  showInterval: boolean;
  interval: number;
  trainRange: TrainTimeRange;
  forecastRange: TrainTimeRange;
  /** Max overlay forecast loads in flight for this panel. Default 1, minimum 1. */
  maxInflightLoads?: number;
  /** Series the alert rule is built for. Empty means the panel's only series. */
  alertSeries?: string;
  /** Alert window ending at now, e.g. `10m`; empty uses the 10-minute default. */
  alertLookback?: string;
  /** Percent deviation the rule's Threshold fires above. Default 20. */
  alertTolerance?: number;
}

/**
 * Prediction-interval coverage bounds. The overlay's Interval coverage option and the
 * Forecast datasource's Coverage input are the same knob, so they share one rule.
 */
export const COVERAGE_SETTINGS = { min: 0, max: 0.99, step: 0.05 } as const;

export interface ForecastResponse {
  times?: number[];
  values?: Array<number | null>;
  lower?: Array<number | null>;
  upper?: Array<number | null>;
  needTrain?: boolean;
  cached?: boolean;
}
