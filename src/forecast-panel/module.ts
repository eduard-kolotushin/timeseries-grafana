import { PanelPlugin } from '@grafana/data';
import { ForecastOptions } from './types';
import { ForecastPanel } from './ForecastPanel';
import { TrainRangeEditor } from './TrainRangeEditor';
import { RetrainEditor } from './RetrainEditor';

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
    .addCustomEditor({
      id: 'forecastRange',
      path: 'forecastRange',
      name: 'Forecast range',
      description: 'From and To for forecast timestamps. Auto starts at dashboard now and lasts a model-based duration.',
      defaultValue: { from: '', to: '' },
      editor: TrainRangeEditor,
      settings: { kind: 'forecast' },
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
      description: 'Hour of day, day of week, hour of week, or minute of week',
      defaultValue: 'hour',
      settings: {
        options: [
          { value: 'hour', label: 'Hour' },
          { value: 'day', label: 'Day' },
          { value: 'week', label: 'Week (hour of week)' },
          { value: 'minute-week', label: 'Week (minute of week)' },
        ],
      },
      showIf: (opts) => opts.model === 'baseline',
    })
    .addSelect({
      path: 'calendar',
      name: 'Calendar',
      description: 'Optional production calendar. Only years present in the file apply (RU is 2026); other years behave as Off.',
      defaultValue: '',
      settings: {
        options: [
          { value: '', label: 'Off' },
          { value: 'ru', label: 'RU' },
        ],
      },
      showIf: (opts) => opts.model === 'baseline',
    })
    .addBooleanSwitch({
      path: 'showInterval',
      name: 'Show prediction interval',
      description: 'Overlay a Hyndman Gaussian band on the forecast',
      defaultValue: true,
    })
    .addNumberInput({
      path: 'interval',
      name: 'Interval coverage',
      description: 'Coverage in (0, 1). 0 hides the band.',
      defaultValue: 0.95,
      settings: { min: 0, max: 0.99, step: 0.05 },
      showIf: (opts) => opts.showInterval !== false,
    })
    .addCustomEditor({
      id: 'trainRange',
      path: 'trainRange',
      name: 'Training period',
      description: 'From and To for the fit, independent of the panel time range. Opens above the control. Auto uses the model window.',
      defaultValue: { from: '', to: '' },
      editor: TrainRangeEditor,
      settings: { kind: 'train' },
    })
    .addCustomEditor({
      id: 'retrain',
      path: 'trainRange',
      name: 'Saved model',
      description: 'Reuse the fitted snapshot until Retrain or a query, model, or training-period change. Auto train windows do not re-query as time moves.',
      defaultValue: { from: '', to: '' },
      editor: RetrainEditor,
    });
});
