import { PanelOptionsEditorBuilder, standardEditorsRegistry, StandardEditorContext } from '@grafana/data';
import { plugin } from './module';
import { ForecastOptions } from './types';

// Grafana core registers the standard option editors when its app boots; the options
// builder resolves `addTextInput`/`addSelect`/… against that registry, so a unit test has
// to register them before it can build the options this plugin declares.
standardEditorsRegistry.setInit(() =>
  ['text', 'number', 'select', 'boolean'].map((id) => ({
    id,
    name: id,
    description: id,
    editor: () => null,
  }))
);

/** The panel options the plugin registers, as Grafana's options pane builds them. */
function panelOptions() {
  const builder = new PanelOptionsEditorBuilder<ForecastOptions>();
  const context: StandardEditorContext<ForecastOptions> = { data: [] };
  plugin.getPanelOptionsSupplier()(builder, context);
  return builder.getItems();
}

describe('panel option groups', () => {
  it('registers every option under its group, in Training → Forecast → Overlay → Alerting order', () => {
    // Grafana renders each first-level category as its own section, in the order the
    // category first appears here; this sequence is the whole pane contract.
    const items = panelOptions().map((option) => ({ path: option.path, category: option.category?.[0] ?? null }));
    expect(items).toEqual([
      { path: 'model', category: 'Training' },
      { path: 'alpha', category: 'Training' },
      { path: 'beta', category: 'Training' },
      { path: 'period', category: 'Training' },
      { path: 'season', category: 'Training' },
      { path: 'calendar', category: 'Training' },
      { path: 'trainRange', category: 'Training' },
      { path: 'retrainAction', category: 'Training' },
      { path: 'cacheKeysAction', category: 'Training' },
      { path: 'forecastRange', category: 'Forecast' },
      { path: 'interval', category: 'Forecast' },
      { path: 'showInterval', category: 'Overlay' },
      { path: 'maxInflightLoads', category: 'Overlay' },
      { path: 'alertSeries', category: 'Alerting' },
      { path: 'alertLookback', category: 'Alerting' },
      { path: 'alertTolerance', category: 'Alerting' },
      { path: 'alertAction', category: 'Alerting' },
    ]);
  });

  it('starts a new panel on the seasonal minute-of-week baseline', () => {
    const items = panelOptions();
    expect(items.find((option) => option.path === 'model')?.defaultValue).toBe('baseline');
    expect(items.find((option) => option.path === 'season')?.defaultValue).toBe('minute-week');
  });
});
