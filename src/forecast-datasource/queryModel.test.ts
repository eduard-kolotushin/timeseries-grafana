import { webcrypto } from 'crypto';
import { cacheKey, fingerprintPayload } from '../forecast-panel/cacheKey';
import { ForecastOptions } from '../forecast-panel/types';
import {
  cacheKeyInputFromQuery,
  copySourceFromSibling,
  optionsFromQuery,
  siblingMetricQueries,
  withCacheKey,
} from './queryModel';
import { defaultForecastQuery, ForecastDataQuery, FORECAST_DATASOURCE_TYPE } from './types';

Object.defineProperty(globalThis, 'crypto', { value: webcrypto, configurable: true });

const target = { refId: 'A', datasource: { uid: 'prom', type: 'prometheus' }, expr: 'up' };

const overlayOptions: ForecastOptions = {
  model: 'holt',
  alpha: 0.8,
  beta: 0.2,
  period: 7,
  season: 'hour',
  calendar: '',
  showInterval: true,
  interval: 0.95,
  trainRange: { from: 'now-7d', to: 'now' },
  forecastRange: { from: '', to: '' },
};

function query(partial: Partial<ForecastDataQuery> = {}): ForecastDataQuery {
  return {
    refId: 'B',
    ...defaultForecastQuery,
    model: 'holt',
    trainRange: { from: 'now-7d', to: 'now' },
    seriesName: 'up',
    sourceTargets: [target],
    ...partial,
  };
}

describe('cacheKeyInputFromQuery', () => {
  it('matches overlay fingerprintPayload for the same targets and fit options', () => {
    const q = query();
    const fromQuery = fingerprintPayload(cacheKeyInputFromQuery(q));
    const fromOverlay = fingerprintPayload({
      targets: [target],
      options: overlayOptions,
      seriesName: 'up',
    });
    expect(fromQuery).toEqual(fromOverlay);
  });

  it('does not include forecast window or coverage in the fingerprint', () => {
    const a = fingerprintPayload(cacheKeyInputFromQuery(query({ level: 0.8 })));
    const b = fingerprintPayload(cacheKeyInputFromQuery(query({ level: 0.99 })));
    expect(a).toEqual(b);
  });

  it('changes when the source expr or train-range strings change', () => {
    const base = fingerprintPayload(cacheKeyInputFromQuery(query()));
    expect(
      fingerprintPayload(
        cacheKeyInputFromQuery(query({ sourceTargets: [{ ...target, expr: 'up + 1' }] }))
      )
    ).not.toEqual(base);
    expect(
      fingerprintPayload(
        cacheKeyInputFromQuery(query({ trainRange: { from: 'now-21d', to: 'now' } }))
      )
    ).not.toEqual(base);
  });
});

describe('withCacheKey', () => {
  it('writes the same 64-hex key as overlay cacheKey', async () => {
    const q = query();
    const dsKey = (await withCacheKey(q)).cacheKey;
    const overlayKey = await cacheKey({
      targets: [target],
      options: overlayOptions,
      seriesName: 'up',
    });
    expect(dsKey).toBe(overlayKey);
    expect(dsKey).toMatch(/^[a-f0-9]{64}$/);
  });
});

describe('optionsFromQuery', () => {
  it('fills overlay option defaults used by the fingerprint', () => {
    const opts = optionsFromQuery(query());
    expect(opts.model).toBe('holt');
    expect(opts.trainRange).toEqual({ from: 'now-7d', to: 'now' });
  });
});

describe('siblingMetricQueries', () => {
  it('drops self, hidden, and Forecast datasource rows', () => {
    expect(
      siblingMetricQueries(
        [
          { refId: 'A', datasource: { uid: 'prom', type: 'prometheus' } },
          { refId: 'B', datasource: { uid: 'fc', type: FORECAST_DATASOURCE_TYPE } },
          { refId: 'C', hide: true, datasource: { uid: 'prom', type: 'prometheus' } },
        ],
        'B'
      ).map((q) => q.refId)
    ).toEqual(['A']);
  });
});

describe('copySourceFromSibling', () => {
  it('copies sourceTargets only', () => {
    const sibling = {
      refId: 'A',
      datasource: { uid: 'druid', type: 'grafadruid-druid-datasource' },
      rawSql: 'SELECT 1',
    };
    const q = query({
      model: 'baseline',
      season: 'minute-week',
      seriesName: 'keep',
      trainRange: { from: 'now-21d', to: 'now' },
      sourceTargets: [],
    });
    const next = copySourceFromSibling(q, sibling);
    expect(next.model).toBe('baseline');
    expect(next.season).toBe('minute-week');
    expect(next.seriesName).toBe('keep');
    expect(next.trainRange).toEqual({ from: 'now-21d', to: 'now' });
    expect(next.sourceTargets).toEqual([
      {
        refId: 'A',
        rawSql: 'SELECT 1',
        datasource: { uid: 'druid', type: 'grafadruid-druid-datasource' },
      },
    ]);
  });

  it('fingerprint matches overlay for the same A after copy', () => {
    const copied = copySourceFromSibling(
      query({ sourceTargets: [], seriesName: 'up', model: 'holt', trainRange: overlayOptions.trainRange }),
      target
    );
    expect(fingerprintPayload(cacheKeyInputFromQuery(copied))).toEqual(
      fingerprintPayload({
        targets: [target],
        options: overlayOptions,
        seriesName: 'up',
      })
    );
  });
});
