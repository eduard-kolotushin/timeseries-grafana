import { PanelOptionsEditorBuilder, standardEditorsRegistry, StandardEditorContext } from '@grafana/data';
import { resolveTrainWindow } from './lookback';
import { plugin } from './module';
import { ForecastOptions } from './types';

const DAY_MS = 86_400_000;

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

describe('the legacy lookback option', () => {
  it('is editable, so a panel written before the training-period picker can be reproduced', () => {
    const item = panelOptions().find((option) => option.path === 'lookback');
    // The cacheKey always includes `lookback`, so without an editor a stored value can
    // never be put back and the key its alert query carries is unreachable from the UI.
    expect(item).toBeDefined();
    expect(item?.name).toBe('Legacy lookback');
    expect(item?.defaultValue).toBe('');
  });

  // The value the option edits is the one the panel path reads: with no saved training
  // period, `resolveTrainWindow` resolves the window from exactly this field.
  const now = Date.UTC(2026, 0, 8);
  it.each<[string, number]>([
    ['21d', 21 * DAY_MS],
    ['5', 5 * DAY_MS],
    ['auto', 14 * DAY_MS],
    ['', 14 * DAY_MS],
    ['nonsense', 14 * DAY_MS],
  ])('lookback %s resolves a window %s ms wide', (lookback, lookbackMs) => {
    expect(resolveTrainWindow({ model: 'baseline', season: 'hour', lookback }, now)).toEqual({
      fromMs: now - lookbackMs,
      toMs: now,
      relative: true,
      lookbackMs,
    });
  });
});
