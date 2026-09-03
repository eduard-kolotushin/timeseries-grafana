import { FORECAST_DATASOURCE_TYPE } from '../forecast-datasource/types';
import {
  alertingNewPath,
  asSaveModel,
  dashboardTimeFromScene,
  dashboardUidFromPath,
  EXPRESSION_DATASOURCE_UID,
  findPanel,
  findQueryRunnerState,
  pickLivePanel,
  REASON_DASHBOARD_NOT_SAVED,
  REASON_NO_ALERTING_QUERY,
  ruleFormDefaultsFromPanel,
} from './alertFromPanel';

const metric = {
  refId: 'A',
  datasource: { uid: 'testdata', type: 'grafana-testdata-datasource' },
  scenarioId: 'random_walk',
};
const forecast = {
  refId: 'B',
  datasource: { uid: 'fc', type: FORECAST_DATASOURCE_TYPE },
  kind: 'forecast' as const,
  sourceTargets: [metric],
  cacheKey: 'ab'.repeat(32),
};

describe('dashboardUidFromPath', () => {
  it('reads /d/:uid', () => {
    expect(dashboardUidFromPath('/d/forecast-demo/forecast-demo')).toBe('forecast-demo');
    expect(dashboardUidFromPath('/dashboard/new')).toBeUndefined();
  });
});

describe('asSaveModel / findPanel', () => {
  it('unwraps { dashboard } and finds nested row panels', () => {
    const inner = { id: 2, title: 'Inner', targets: [metric] };
    const raw = { dashboard: { uid: 'd1', panels: [{ id: 1, collapsed: true, panels: [inner] }] } };
    const model = asSaveModel(raw);
    expect(model?.uid).toBe('d1');
    expect(findPanel(model?.panels, 2)).toEqual(inner);
  });
});

describe('ruleFormDefaultsFromPanel', () => {
  const alertingUids = new Set(['fc', 'prom']);

  it('keeps Mixed Forecast targets and drops hidden and non-alerting rows', () => {
    const got = ruleFormDefaultsFromPanel({
      dashboardUid: 'dash',
      panelId: 1,
      panel: {
        id: 1,
        title: 'Holt overlay',
        targets: [metric, forecast, { ...metric, refId: 'C', hide: true }, { ...metric, refId: 'D', datasource: { uid: 'prom', type: 'prometheus' } }],
      },
      alertingUids,
    });
    expect(got.ok).toBe(true);
    if (!got.ok) {
      return;
    }
    const data = got.defaults.queries.filter((q) => q.datasourceUid !== EXPRESSION_DATASOURCE_UID);
    expect(data.map((q) => q.refId)).toEqual(['B', 'D']);
    expect(data[0].model.kind).toBe('forecast');
    expect(got.defaults.type).toBe('grafana-alerting');
    expect(got.defaults.name).toBe('Holt overlay');
    expect(got.defaults.annotations).toEqual([
      { key: '__dashboardUid__', value: 'dash' },
      { key: '__panelId__', value: '1' },
    ]);
    expect(got.defaults.queries.some((q) => q.model.type === 'reduce')).toBe(true);
    expect(got.defaults.queries.some((q) => q.model.type === 'threshold')).toBe(true);
    expect(got.defaults.condition).toBe(got.defaults.queries[got.defaults.queries.length - 1].refId);
  });

  it('reasons when the dashboard is unsaved', () => {
    expect(ruleFormDefaultsFromPanel({ panel: { id: 1, targets: [forecast] }, alertingUids })).toEqual({
      ok: false,
      reason: REASON_DASHBOARD_NOT_SAVED,
    });
  });

  it('reasons when no alerting-capable query remains', () => {
    expect(
      ruleFormDefaultsFromPanel({
        dashboardUid: 'dash',
        panelId: 1,
        panel: { id: 1, targets: [metric] },
        alertingUids,
      })
    ).toEqual({ ok: false, reason: REASON_NO_ALERTING_QUERY });
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
                queries: [metric, forecast],
                datasource: { uid: '-- Mixed --', type: 'datasource' },
                maxDataPoints: 100,
              },
            },
          },
        },
      },
    };
    expect(findQueryRunnerState(root)).toEqual({
      queries: [metric, forecast],
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
    const saved = { id: 1, title: 'saved', targets: [metric] };
    const scene = {
      id: 1,
      title: 'Holt overlay',
      targets: [metric, forecast],
      datasource: { uid: '-- Mixed --', type: 'datasource' },
    };
    expect(pickLivePanel(saved, scene, 1).targets).toEqual([metric, forecast]);
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
