import { PanelPlugin } from '@grafana/data';
import { COVERAGE_SETTINGS, ForecastOptions } from './types';
import { ForecastPanel } from './ForecastPanel';
import { TrainRangeEditor } from './TrainRangeEditor';
import { RetrainEditor } from './RetrainEditor';
import { AlertEditor } from './AlertEditor';
import { AlertSeriesEditor } from './AlertSeriesEditor';

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
      settings: { ...COVERAGE_SETTINGS },
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
    .addTextInput({
      path: 'lookback',
      name: 'Legacy lookback',
      description:
        'Duration lookback (e.g. 21d, or a bare day count) from before the Training period picker. Only a panel that never saved a training period trains from it, and the cacheKey always includes it, so keep a legacy panel’s stored value to reproduce the key its alert query carries. Empty is Auto: the model window.',
      defaultValue: '',
    })
    .addNumberInput({
      path: 'maxInflightLoads',
      name: 'Max in-flight loads',
      description: 'How many overlay forecast loads this panel may run at once. Extra refreshes cancel the oldest. Minimum 1.',
      defaultValue: 1,
      settings: { min: 1, integer: true },
    })
    // Button-only editors. They never call onChange; each has its own unused
    // path so a future onChange cannot clobber a real option like trainRange.
    .addCustomEditor({
      id: 'retrain',
      path: 'retrainAction',
      name: 'Saved model',
      description: 'Reuse the fitted snapshot until Retrain or a query, model, or training-period change. Auto train windows do not re-query as time moves.',
      editor: RetrainEditor,
    })
    .addCustomEditor({
      id: 'alertSeries',
      path: 'alertSeries',
      name: 'Alert series',
      description:
        'Which metric series the alert rule is for; Grafana pairs the baseline with it by label, or the rule selects it by name where the series carry no labels. Shown only when the panel has more than one.',
      category: ['Alerting'],
      editor: AlertSeriesEditor,
    })
    .addTextInput({
      path: 'alertLookback',
      name: 'Alert lookback',
      description: 'Both rule queries read now - this … now. Empty is 10m.',
      defaultValue: '',
      category: ['Alerting'],
    })
    .addNumberInput({
      path: 'alertTolerance',
      name: 'Alert tolerance',
      description: 'Percent deviation from the baseline the rule fires above.',
      defaultValue: 20,
      settings: { min: 0 },
      category: ['Alerting'],
    })
    .addCustomEditor({
      id: 'alertFromPanel',
      path: 'alertAction',
      name: 'New alert rule',
      description:
        'Build a Grafana alert rule from this panel: its metric query plus the trained baseline over now - lookback … now, with a percent-deviation threshold. Save the dashboard first. Existing rules stay on the Alerting page.',
      category: ['Alerting'],
      editor: AlertEditor,
    });
});
