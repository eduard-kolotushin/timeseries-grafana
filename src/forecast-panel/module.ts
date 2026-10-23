import { PanelPlugin } from '@grafana/data';
import { COVERAGE_SETTINGS, DEFAULT_FORECAST_OPTIONS, ForecastOptions } from './types';
import { ForecastPanel } from './ForecastPanel';
import { TrainRangeEditor } from './TrainRangeEditor';
import { RetrainEditor } from './RetrainEditor';
import { AlertEditor } from './AlertEditor';
import { AlertSeriesEditor } from './AlertSeriesEditor';
import { CacheKeysEditor } from './CacheKeysEditor';

export const plugin = new PanelPlugin<ForecastOptions>(ForecastPanel).setPanelOptions((builder) => {
  return builder
    .addSelect({
      path: 'model',
      name: 'Model',
      description: 'Model fitted to the training window.',
      defaultValue: DEFAULT_FORECAST_OPTIONS.model,
      category: ['Training'],
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
      path: 'alpha',
      name: 'Alpha',
      description: 'Level smoothing (SES, Holt), 0–1.',
      defaultValue: DEFAULT_FORECAST_OPTIONS.alpha,
      category: ['Training'],
      settings: { min: 0.001, max: 1, step: 0.05 },
      showIf: (opts) => opts.model === 'ses' || opts.model === 'holt',
    })
    .addNumberInput({
      path: 'beta',
      name: 'Beta',
      description: 'Trend smoothing (Holt), 0–1.',
      defaultValue: DEFAULT_FORECAST_OPTIONS.beta,
      category: ['Training'],
      settings: { min: 0.001, max: 1, step: 0.05 },
      showIf: (opts) => opts.model === 'holt',
    })
    .addNumberInput({
      path: 'period',
      name: 'Seasonal period',
      description: 'Points per seasonal cycle, e.g. 7.',
      defaultValue: DEFAULT_FORECAST_OPTIONS.period,
      category: ['Training'],
      settings: { min: 1, integer: true },
      showIf: (opts) => opts.model === 'seasonal',
    })
    .addSelect({
      path: 'season',
      name: 'Seasonality',
      description: 'Seasonal bucket for the baseline, e.g. minute of week for 1-minute data.',
      defaultValue: DEFAULT_FORECAST_OPTIONS.season,
      category: ['Training'],
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
      description: 'Holiday calendar for the baseline; RU covers 2026 only.',
      defaultValue: DEFAULT_FORECAST_OPTIONS.calendar,
      category: ['Training'],
      settings: {
        options: [
          { value: '', label: 'Off' },
          { value: 'ru', label: 'RU' },
        ],
      },
      showIf: (opts) => opts.model === 'baseline',
    })
    .addCustomEditor({
      id: 'trainRange',
      path: 'trainRange',
      name: 'Training period',
      description: 'Fit window, independent of the dashboard range. Empty is Auto.',
      defaultValue: { ...DEFAULT_FORECAST_OPTIONS.trainRange },
      category: ['Training'],
      editor: TrainRangeEditor,
      settings: { kind: 'train' },
    })
    // Editors that never call onChange — the two buttons and the read-only key list.
    // Each has its own unused path so a future onChange cannot clobber a real option
    // like trainRange.
    .addCustomEditor({
      id: 'retrain',
      path: 'retrainAction',
      name: 'Saved model',
      description: 'Reuse the saved fit until Retrain or a model/training-window change.',
      category: ['Training'],
      editor: RetrainEditor,
    })
    .addCustomEditor({
      id: 'cacheKeys',
      path: 'cacheKeysAction',
      name: 'Baseline keys',
      description: 'Cache key per series; copy one into a Forecast query or alert rule.',
      category: ['Training'],
      editor: CacheKeysEditor,
    })
    .addCustomEditor({
      id: 'forecastRange',
      path: 'forecastRange',
      name: 'Forecast range',
      description: 'Forecast window; empty is Auto (now → now + model horizon).',
      defaultValue: { ...DEFAULT_FORECAST_OPTIONS.forecastRange },
      category: ['Forecast'],
      editor: TrainRangeEditor,
      settings: { kind: 'forecast' },
    })
    .addNumberInput({
      path: 'interval',
      name: 'Interval coverage',
      description: 'Coverage in (0, 1), e.g. 0.95. 0 hides the band.',
      defaultValue: DEFAULT_FORECAST_OPTIONS.interval,
      category: ['Forecast'],
      settings: { ...COVERAGE_SETTINGS },
      showIf: (opts) => opts.showInterval !== false,
    })
    .addBooleanSwitch({
      path: 'showInterval',
      name: 'Show prediction interval',
      description: 'Draw the Gaussian prediction band.',
      defaultValue: DEFAULT_FORECAST_OPTIONS.showInterval,
      category: ['Overlay'],
    })
    .addNumberInput({
      path: 'maxInflightLoads',
      name: 'Max in-flight loads',
      description: 'Concurrent forecast loads; extra refreshes cancel the oldest.',
      defaultValue: DEFAULT_FORECAST_OPTIONS.maxInflightLoads,
      category: ['Overlay'],
      settings: { min: 1, integer: true },
    })
    .addCustomEditor({
      id: 'alertSeries',
      path: 'alertSeries',
      name: 'Alert series',
      description: 'Series the rule watches; shown when the panel has more than one.',
      defaultValue: DEFAULT_FORECAST_OPTIONS.alertSeries,
      category: ['Alerting'],
      editor: AlertSeriesEditor,
    })
    .addTextInput({
      path: 'alertLookback',
      name: 'Alert lookback',
      description: 'Both rule queries read now - this … now. Defaults to 10m.',
      defaultValue: DEFAULT_FORECAST_OPTIONS.alertLookback,
      settings: { placeholder: '10m' },
      category: ['Alerting'],
    })
    .addNumberInput({
      path: 'alertTolerance',
      name: 'Alert tolerance',
      description: 'Percent deviation from the baseline that fires the rule.',
      defaultValue: DEFAULT_FORECAST_OPTIONS.alertTolerance,
      settings: { min: 0 },
      category: ['Alerting'],
    })
    .addCustomEditor({
      id: 'alertFromPanel',
      path: 'alertAction',
      name: 'New alert rule',
      description: 'Create a rule from this panel’s query and saved baseline. Save the dashboard first.',
      category: ['Alerting'],
      editor: AlertEditor,
    });
});
