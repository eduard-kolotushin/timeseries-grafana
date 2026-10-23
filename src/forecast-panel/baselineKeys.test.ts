import { webcrypto } from 'crypto';
import { baselineKeys } from './baselineKeys';
import { cacheKey } from './cacheKey';
import { metricTargets } from './mixed';
import { DEFAULT_FORECAST_OPTIONS } from './types';

Object.defineProperty(globalThis, 'crypto', { value: webcrypto, configurable: true });

const druid = { refId: 'A', datasource: { uid: 'druid', type: 'druid' }, queryType: 'sql', rawSql: 'SELECT 1' };
const hidden = { ...druid, refId: 'B', hide: true, rawSql: 'SELECT 2' };
const forecastRow = { refId: 'C', datasource: { uid: 'fc', type: 'eduardkolotushin-forecast-datasource' }, cacheKey: 'a'.repeat(64) };
const options = {
  ...DEFAULT_FORECAST_OPTIONS,
  model: 'baseline' as const,
  season: 'minute-week' as const,
  trainRange: { from: 'now-21d', to: 'now' },
};
const visibleFromMs = 1_700_000_000_000;
const visibleToMs = 1_700_000_600_000;

describe('baselineKeys', () => {
  it('computes the overlay key per series, over the metric targets only', async () => {
    const rows = await baselineKeys({
      targets: [druid, hidden, forecastRow],
      names: ['value', 'value 2'],
      options,
      visibleFromMs,
      visibleToMs,
    });
    const targets = metricTargets([druid, hidden, forecastRow]);
    expect(rows).toEqual([
      { name: 'value', key: await cacheKey({ targets, options, seriesName: 'value', visibleFromMs, visibleToMs }) },
      { name: 'value 2', key: await cacheKey({ targets, options, seriesName: 'value 2', visibleFromMs, visibleToMs }) },
    ]);
    // Hidden rows and Forecast rows are not part of the fingerprint the overlay writes.
    expect(rows[0].key).not.toBe(await cacheKey({ targets: [druid, hidden, forecastRow], options, seriesName: 'value', visibleFromMs, visibleToMs }));
  });

  it('returns no rows for a panel with no series', async () => {
    expect(await baselineKeys({ targets: [druid], names: [], options })).toEqual([]);
  });
});
