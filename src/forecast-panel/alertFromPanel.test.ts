import { webcrypto } from 'crypto';
import { FieldType, toDataFrame } from '@grafana/data';
import { FORECAST_DATASOURCE_TYPE } from '../forecast-datasource/types';
import { cacheKey } from './cacheKey';
import { extractSeries } from './extract';
import { ForecastOptions } from './types';
import {
  absoluteRangeMs,
  alertingNewPath,
  alertingUidsFor,
  asSaveModel,
  dashboardTimeFromScene,
  dashboardUidFromPath,
  EXPRESSION_DATASOURCE_TYPE,
  EXPRESSION_DATASOURCE_UID,
  findPanel,
  findQueryRunnerState,
  forecastDatasourceUid,
  LivePanel,
  livePanel,
  metricAlertTargets,
  PanelSeries,
  PanelTarget,
  panelSeries,
  pickLivePanel,
  REASON_ALERT_SERIES_MISSING,
  REASON_ALERT_SERIES_REQUIRED,
  REASON_ALERT_SERIES_UNLABELLED,
  REASON_DASHBOARD_NOT_SAVED,
  REASON_FORECAST_DATASOURCE,
  REASON_NO_ALERTING_QUERY,
  ruleDefaultsFromPanel,
  windowFrom,
} from './alertFromPanel';

Object.defineProperty(globalThis, 'crypto', { value: webcrypto, configurable: true });

jest.mock('@grafana/runtime', () => ({
  getDataSourceSrv: () => ({
    getInstanceSettings: (uid: string) =>
      ({ druid: { meta: { alerting: true } }, loki: { meta: { alerting: false } } } as Record<
        string,
        { meta: { alerting: boolean } } | undefined
      >)[uid],
    getList: () => [{ uid: 'fc', type: 'eduardkolotushin-forecast-datasource' }],
  }),
}));

const druid = {
  refId: 'A',
  datasource: { uid: 'druid', type: 'grafadruid-druid-datasource' },
  queryType: 'sql',
  rawSql: 'SELECT 1',
};
const forecastRow = { refId: 'B', datasource: { uid: 'fc', type: FORECAST_DATASOURCE_TYPE } };

const options: ForecastOptions = {
  model: 'baseline',
  alpha: 0.8,
  beta: 0.2,
  period: 7,
  season: 'minute-week',
  calendar: '',
  showInterval: true,
  interval: 0.95,
  trainRange: { from: 'now-21d', to: 'now' },
  forecastRange: { from: '', to: '' },
};

const visibleFromMs = 1_700_000_000_000;
const visibleToMs = 1_700_000_600_000;
const series: PanelSeries[] = [{ name: 'p95', refId: 'A', labels: { host: 'a' } }];

type BuildInput = {
  dashboardUid?: string;
  panel?: LivePanel;
  panelId?: number;
  options?: ForecastOptions;
  series?: PanelSeries[];
  metricTargets?: PanelTarget[];
  alertingUids?: Set<string>;
  forecastDatasourceUid?: string;
};

function build(over: BuildInput = {}) {
  return ruleDefaultsFromPanel({
    dashboardUid: 'dash',
    panelId: 3,
    panel: { id: 3, title: 'Seasonal baseline', targets: [druid], maxDataPoints: 15000 },
    options,
    series,
    metricTargets: [druid],
    visibleFromMs,
    visibleToMs,
    alertingUids: new Set(['druid']),
    forecastDatasourceUid: 'fc',
    ...over,
  });
}

/** The rule's data rows: the panel's metric rows plus the one synthesized Forecast row. */
function dataRows(queries: Array<{ refId: string; datasourceUid: string }>) {
  return queries.filter((q) => q.datasourceUid !== EXPRESSION_DATASOURCE_UID);
}

describe('dashboardUidFromPath', () => {
  it('reads /d/:uid', () => {
    expect(dashboardUidFromPath('/d/forecast-demo/forecast-demo')).toBe('forecast-demo');
    expect(dashboardUidFromPath('/dashboard/new')).toBeUndefined();
  });
});

describe('asSaveModel / findPanel', () => {
  it('unwraps { dashboard } and finds nested row panels', () => {
    const inner = { id: 2, title: 'Inner', targets: [druid] };
    const raw = { dashboard: { uid: 'd1', panels: [{ id: 1, collapsed: true, panels: [inner] }] } };
    const model = asSaveModel(raw);
    expect(model?.uid).toBe('d1');
    expect(findPanel(model?.panels, 2)).toEqual(inner);
  });
});

describe('alertingUidsFor', () => {
  const expr = { refId: 'B', datasource: { uid: EXPRESSION_DATASOURCE_UID, type: EXPRESSION_DATASOURCE_TYPE } };

  const cases: Array<[string, PanelTarget[], { uid: string } | undefined, string[]]> = [
    ['a row on a datasource that can alert', [druid], undefined, ['druid']],
    ['a row that inherits the panel datasource', [{ refId: 'A' }], { uid: 'druid' }, ['druid']],
    ['a row on a datasource that cannot alert', [{ refId: 'C', datasource: { uid: 'loki' } }], undefined, []],
    ['a row with no datasource anywhere', [{ refId: 'A' }], undefined, []],
    // A panel expression row is not a datasource the rule may draw on: the rule builds its
    // own reduce/math/threshold chain, so forwarding one would put unrelated nodes in it.
    ['a panel expression row', [expr], undefined, []],
  ];

  it.each(cases)('%s → %s', (_name, targets, panelDs, want) => {
    expect([...alertingUidsFor(targets, panelDs)]).toEqual(want);
  });
});

describe('windowFrom', () => {
  it.each<[string | undefined, { from: number; to: number }]>([
    ['10m', { from: 600, to: 0 }],
    ['600s', { from: 600, to: 0 }],
    ['1h', { from: 3600, to: 0 }],
    ['600', { from: 600, to: 0 }],
    ['nonsense', { from: 600, to: 0 }],
    ['0', { from: 600, to: 0 }],
    ['', { from: 600, to: 0 }],
    [undefined, { from: 600, to: 0 }],
  ])('%s → %j', (lookback, want) => {
    expect(windowFrom(lookback)).toEqual(want);
  });
});

describe('panelSeries', () => {
  const frame = (refId = 'A') =>
    toDataFrame({
      refId,
      fields: [
        { name: 'Time', type: FieldType.time, values: [0, 1000] },
        { name: 'Value', type: FieldType.number, values: [1, 2], labels: { host: 'a' } },
      ],
    });

  it('names the panel’s metric series as the fit does, with labels and the frame’s refId', () => {
    const f = frame();
    expect(panelSeries([f], [druid])).toEqual([
      { name: extractSeries(f, [f])[0].name, refId: 'A', labels: { host: 'a' } },
    ]);
  });

  it('skips a Forecast row’s own frames', () => {
    expect(panelSeries([frame('B')], [forecastRow])).toEqual([]);
  });

  it('mentions each series once', () => {
    const f = frame();
    expect(panelSeries([f, f], [druid])).toHaveLength(1);
  });
});

describe('metricAlertTargets', () => {
  const uids = new Set(['druid']);

  it.each<[string, PanelTarget[], string[]]>([
    ['an alerting metric row', [druid], ['A']],
    ['a hidden row', [{ ...druid, hide: true }], []],
    ['a Forecast row', [forecastRow], []],
    ['a panel expression row', [{ refId: 'C', datasource: { uid: EXPRESSION_DATASOURCE_UID } }], []],
    ['a row on a datasource that cannot alert', [{ refId: 'D', datasource: { uid: 'loki' } }], []],
    ['a row with no datasource anywhere', [{ refId: 'E' }], []],
  ])('%s', (_name, targets, want) => {
    expect(metricAlertTargets(targets, undefined, uids).map((t) => t.refId)).toEqual(want);
  });

  it('resolves a row that inherits the panel datasource', () => {
    expect(metricAlertTargets([{ refId: 'E' }], { uid: 'druid' }, uids).map((t) => t.refId)).toEqual(['E']);
  });
});

describe('forecastDatasourceUid', () => {
  const srv = (list: Array<{ uid: string; type?: string }>) => ({ getList: () => list });
  const instance = { uid: 'fc', type: FORECAST_DATASOURCE_TYPE };

  it('prefers the panel’s own Forecast row', () => {
    expect(forecastDatasourceUid([{ refId: 'B', datasource: { uid: 'fc2', type: FORECAST_DATASOURCE_TYPE } }], srv([instance]))).toBe(
      'fc2'
    );
  });

  it('falls back to the deployment’s only Forecast instance', () => {
    expect(forecastDatasourceUid([druid], srv([instance]))).toBe('fc');
  });

  it('is undefined with two instances and no panel row', () => {
    expect(forecastDatasourceUid([druid], srv([instance, { uid: 'fc2', type: FORECAST_DATASOURCE_TYPE }]))).toBeUndefined();
  });

  it('is undefined with no Forecast instance', () => {
    expect(forecastDatasourceUid([druid], srv([{ uid: 'druid', type: 'grafadruid-druid-datasource' }]))).toBeUndefined();
  });
});

describe('absoluteRangeMs', () => {
  it('turns a raw range into the ms the fingerprint redacts absolute timestamps with', () => {
    const got = absoluteRangeMs({ from: '2026-01-01T00:00:00.000Z', to: '2026-01-01T00:15:00.000Z' });
    expect(got.visibleFromMs).toBe(Date.UTC(2026, 0, 1));
    expect(got.visibleToMs).toBe(Date.UTC(2026, 0, 1, 0, 15));
  });

  it('is empty without a range', () => {
    expect(absoluteRangeMs(undefined)).toEqual({});
    expect(absoluteRangeMs({ from: '', to: '' })).toEqual({});
  });
});

describe('ruleDefaultsFromPanel', () => {
  it('builds metric → reduce → reduce → math → threshold and conditions on the threshold', async () => {
    const got = await build();
    expect(got.ok).toBe(true);
    if (!got.ok) {
      return;
    }
    const queries = got.defaults.queries;
    expect(queries.map((q) => q.refId)).toEqual(['A', 'B', 'C', 'D', 'E', 'F']);
    expect(queries.map((q) => q.queryType)).toEqual(['sql', '', 'expression', 'expression', 'expression', 'expression']);
    expect(queries.map((q) => q.datasourceUid)).toEqual([
      'druid',
      'fc',
      EXPRESSION_DATASOURCE_UID,
      EXPRESSION_DATASOURCE_UID,
      EXPRESSION_DATASOURCE_UID,
      EXPRESSION_DATASOURCE_UID,
    ]);
    // Both data queries — and therefore both reduces — read the same window ending now.
    for (const node of queries) {
      expect(node.relativeTimeRange).toEqual({ from: 600, to: 0 });
    }
    expect(queries[0].model).toMatchObject({ rawSql: 'SELECT 1', refId: 'A', maxDataPoints: 15000 });
    expect(queries[2].model).toMatchObject({ type: 'reduce', reducer: 'last', expression: 'A' });
    expect(queries[3].model).toMatchObject({ type: 'reduce', reducer: 'last', expression: 'B' });
    expect(queries[4].model).toMatchObject({ type: 'math', expression: 'abs(($C - $D) / $D) * 100' });
    expect(queries[5].model).toMatchObject({
      type: 'threshold',
      expression: 'E',
      evaluator: { type: 'gt', params: [20] },
    });
    expect(got.defaults.condition).toBe('F');
    expect(got.defaults.name).toBe('Seasonal baseline');
    expect(got.defaults.annotations).toEqual([
      { key: '__dashboardUid__', value: 'dash' },
      { key: '__panelId__', value: '3' },
    ]);
  });

  it('synthesizes the forecast query from the panel fit and the overlay’s own cacheKey', async () => {
    const got = await build();
    expect(got.ok).toBe(true);
    if (!got.ok) {
      return;
    }
    const fc = got.defaults.queries[1];
    expect(fc.queryType).toBe('');
    expect(fc.model).toMatchObject({
      kind: 'forecast',
      seriesName: 'p95',
      model: 'baseline',
      season: 'minute-week',
      alpha: 0.8,
      beta: 0.2,
      period: 7,
      calendar: '',
      level: 0.95,
      trainRange: { from: 'now-21d', to: 'now' },
      sourceTargets: [druid],
      seriesLabels: { host: 'a' },
      datasource: { uid: 'fc', type: FORECAST_DATASOURCE_TYPE },
      maxDataPoints: 15000,
    });
    // The whole point of the synthesized row: the panel's own fit key, so the rule restores
    // the trained snapshot instead of answering needTrain.
    expect(fc.model.cacheKey).toBe(
      await cacheKey({ targets: [druid], visibleFromMs, visibleToMs, options, seriesName: 'p95' })
    );
    expect(fc.model.cacheKey).toMatch(/^[a-f0-9]{64}$/);
  });

  it('follows the lookback and tolerance options', async () => {
    const got = await build({ options: { ...options, alertLookback: '1h', alertTolerance: 5 } });
    expect(got.ok).toBe(true);
    if (!got.ok) {
      return;
    }
    expect(got.defaults.queries.map((q) => q.relativeTimeRange)).toEqual(Array(6).fill({ from: 3600, to: 0 }));
    expect(got.defaults.queries[5].model.evaluator).toEqual({ type: 'gt', params: [5] });
  });

  it('falls back to the default window for an unparseable lookback', async () => {
    const got = await build({ options: { ...options, alertLookback: 'soon' } });
    expect(got.ok).toBe(true);
    if (!got.ok) {
      return;
    }
    expect(got.defaults.queries[1].relativeTimeRange).toEqual({ from: 600, to: 0 });
  });

  it('rules on the chosen series’ metric row while the key still fingerprints every target', async () => {
    const c = { ...druid, refId: 'C', rawSql: 'SELECT 2' };
    const got = await build({
      panel: { id: 3, title: 'Two series', targets: [druid, c], maxDataPoints: 15000 },
      metricTargets: [druid, c],
      series: [
        { name: 'p95', refId: 'A' },
        { name: 'p99', refId: 'C' },
      ],
      options: { ...options, alertSeries: 'p99' },
    });
    expect(got.ok).toBe(true);
    if (!got.ok) {
      return;
    }
    // The panel's own rows keep their refIds; the synthesized row takes the first free letter
    // (`A`, since only `C` is taken), and the metric reduce reads the chosen series' row.
    expect(dataRows(got.defaults.queries).map((q) => q.refId)).toEqual(['C', 'A']);
    expect(got.defaults.queries[1].model.seriesName).toBe('p99');
    expect(got.defaults.queries[1].model.sourceTargets).toEqual([druid, c]);
    expect(got.defaults.queries[2].model.expression).toBe('C');
    expect(got.defaults.queries[3].model.expression).toBe('A');
  });

  it.each<[string, BuildInput, string]>([
    ['an unsaved dashboard', { dashboardUid: '' }, REASON_DASHBOARD_NOT_SAVED],
    ['no alerting-capable row', { alertingUids: new Set() }, REASON_NO_ALERTING_QUERY],
    ['no loaded series', { series: [] }, REASON_ALERT_SERIES_MISSING],
    [
      'two series and no choice',
      {
        series: [
          { name: 'a', refId: 'A' },
          { name: 'b', refId: 'A' },
        ],
      },
      REASON_ALERT_SERIES_REQUIRED,
    ],
    ['a stale series choice', { options: { ...options, alertSeries: 'gone' } }, REASON_ALERT_SERIES_MISSING],
    [
      'two unlabelled series in one row',
      {
        series: [
          { name: 'A-series', refId: 'A' },
          { name: 'A-series1', refId: 'A' },
        ],
        options: { ...options, alertSeries: 'A-series1' },
      },
      REASON_ALERT_SERIES_UNLABELLED,
    ],
    ['no Forecast datasource instance', { forecastDatasourceUid: undefined }, REASON_FORECAST_DATASOURCE],
  ])('%s → %s', async (_name, over, reason) => {
    expect(await build(over)).toEqual({ ok: false, reason });
  });
});

describe('livePanel', () => {
  const grafanaWindow = () => window as unknown as Record<string, unknown>;

  afterEach(() => {
    delete grafanaWindow().__grafanaSceneContext;
    delete grafanaWindow().grafanaRuntime;
  });

  it('reads the unsaved scene panel, the dashboard uid and the time range', () => {
    grafanaWindow().grafanaRuntime = {
      getDashboardSaveModel: () => ({ uid: 'saved-uid', time: { from: 'now-1h', to: 'now' } }),
    };
    grafanaWindow().__grafanaSceneContext = {
      state: {
        uid: 'scene-uid',
        $timeRange: { state: { value: { raw: { from: 'now-15m', to: 'now' } } } },
        editPanel: {
          state: {
            panelRef: {
              resolve: () => ({
                state: {
                  title: 'Seasonal baseline',
                  $data: {
                    state: {
                      queries: [druid, forecastRow],
                      datasource: { uid: '-- Mixed --', type: 'datasource' },
                      maxDataPoints: 15000,
                    },
                  },
                },
              }),
            },
          },
        },
      },
    };
    const live = livePanel(3);
    expect(live.dashboardUid).toBe('saved-uid');
    expect(live.time).toEqual({ from: 'now-15m', to: 'now' });
    expect(live.panel).toMatchObject({
      id: 3,
      title: 'Seasonal baseline',
      targets: [druid, forecastRow],
      maxDataPoints: 15000,
    });
  });

  it('falls back to the saved model when the scene has no panel', () => {
    grafanaWindow().grafanaRuntime = {
      getDashboardSaveModel: () => ({ uid: 'd1', panels: [{ id: 7, title: 'saved', targets: [druid] }] }),
    };
    const live = livePanel(7);
    expect(live.dashboardUid).toBe('d1');
    expect(live.panel?.title).toBe('saved');
    expect(live.panel?.targets).toEqual([druid]);
  });
});

describe('findQueryRunnerState', () => {
  it('finds nested queries on a scene-like tree', () => {
    const root = {
      state: {
        title: 'Holt overlay',
        $data: {
          state: {
            $data: {
              state: {
                queries: [druid, forecastRow],
                datasource: { uid: '-- Mixed --', type: 'datasource' },
                maxDataPoints: 100,
              },
            },
          },
        },
      },
    };
    expect(findQueryRunnerState(root)).toEqual({
      queries: [druid, forecastRow],
      datasource: { uid: '-- Mixed --', type: 'datasource' },
      maxDataPoints: 100,
    });
  });
});

describe('dashboardTimeFromScene', () => {
  it('reads raw from/to', () => {
    expect(
      dashboardTimeFromScene({
        state: { $timeRange: { state: { value: { raw: { from: 'now-15m', to: 'now' } } } } },
      })
    ).toEqual({ from: 'now-15m', to: 'now' });
  });
});

describe('pickLivePanel', () => {
  it('prefers unsaved scene targets over a stale saved panel', () => {
    const saved = { id: 1, title: 'saved', targets: [druid] };
    const scene = {
      id: 1,
      title: 'Holt overlay',
      targets: [druid, forecastRow],
      datasource: { uid: '-- Mixed --', type: 'datasource' },
    };
    expect(pickLivePanel(saved, scene, 1).targets).toEqual([druid, forecastRow]);
  });
});

describe('alertingNewPath', () => {
  it('encodes defaults and returnTo', () => {
    const path = alertingNewPath(
      {
        type: 'grafana-alerting',
        name: 'p',
        queries: [],
        condition: 'C',
        annotations: [],
      },
      '/d/dash?editPanel=1'
    );
    expect(path.startsWith('/alerting/new?')).toBe(true);
    const q = new URLSearchParams(path.slice(path.indexOf('?')));
    expect(JSON.parse(q.get('defaults') ?? '{}').name).toBe('p');
    expect(q.get('returnTo')).toBe('/d/dash?editPanel=1');
  });
});
