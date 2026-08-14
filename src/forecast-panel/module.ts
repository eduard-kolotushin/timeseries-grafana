import { PanelPlugin } from '@grafana/data';
import { ForecastOptions } from './types';
import { ForecastPanel } from './ForecastPanel';

export const plugin = new PanelPlugin<ForecastOptions>(ForecastPanel).setPanelOptions((builder) => {
  return builder
    .addSelect({
      path: 'model',
      name: 'Model',
      description: 'Univariate model from timeseries-forecast',
      defaultValue: 'holt',
      settings: {
        options: [
          { value: 'naive', label: 'Naive' },
          { value: 'mean', label: 'Mean' },
          { value: 'drift', label: 'Drift' },
          { value: 'seasonal', label: 'Seasonal naive' },
          { value: 'baseline', label: 'Seasonal baseline' },
          { value: 'ses', label: 'SES' },
          { value: 'holt', label: 'Holt' },
        ],
      },
    })
    .addNumberInput({
      path: 'horizon',
      name: 'Horizon',
      description: 'Number of future points',
      defaultValue: 10,
      settings: { min: 1, integer: true },
    })
    .addNumberInput({
      path: 'alpha',
      name: 'Alpha',
      description: 'Level smoothing (SES and Holt)',
      defaultValue: 0.8,
      settings: { min: 0.001, max: 1, step: 0.05 },
      showIf: (opts) => opts.model === 'ses' || opts.model === 'holt',
    })
    .addNumberInput({
      path: 'beta',
      name: 'Beta',
      description: 'Trend smoothing (Holt)',
      defaultValue: 0.2,
      settings: { min: 0.001, max: 1, step: 0.05 },
      showIf: (opts) => opts.model === 'holt',
    })
    .addNumberInput({
      path: 'period',
      name: 'Seasonal period',
      defaultValue: 7,
      settings: { min: 1, integer: true },
      showIf: (opts) => opts.model === 'seasonal',
    })
    .addSelect({
      path: 'season',
      name: 'Seasonality',
      description: 'Hour of day, day of week, or hour of week',
      defaultValue: 'hour',
      settings: {
        options: [
          { value: 'hour', label: 'Hour' },
          { value: 'day', label: 'Day' },
          { value: 'week', label: 'Week (hour of week)' },
        ],
      },
      showIf: (opts) => opts.model === 'baseline',
    })
    .addSelect({
      path: 'calendar',
      name: 'Calendar',
      description: 'Optional production calendar for workdays, weekends, and holidays',
      defaultValue: '',
      settings: {
        options: [
          { value: '', label: 'Off' },
          { value: 'ru', label: 'RU' },
        ],
      },
      showIf: (opts) => opts.model === 'baseline',
    });
});
