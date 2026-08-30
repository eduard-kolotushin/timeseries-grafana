import { webcrypto } from 'crypto';
import { cacheKey, fingerprintPayload } from './cacheKey';
import { ForecastOptions } from './types';

Object.defineProperty(globalThis, 'crypto', { value: webcrypto, configurable: true });

const baseOptions: ForecastOptions = {
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

const visFrom = Date.UTC(2026, 7, 20, 10, 0, 0);
const visTo = Date.UTC(2026, 7, 27, 16, 0, 0);

describe('fingerprintPayload', () => {
  it('is stable when only visible range timestamps change', () => {
    const targetA = {
      refId: 'A',
      datasource: { uid: 'prom', type: 'prometheus' },
      expr: 'up',
      interval: '15s',
      rawSql: `SELECT * WHERE ts BETWEEN '${new Date(visFrom).toISOString()}' AND '${new Date(visTo).toISOString()}'`,
    };
    const laterFrom = visFrom + 3600_000;
    const laterTo = visTo + 3600_000;
    const targetB = {
      ...targetA,
      interval: '1m',
      rawSql: `SELECT * WHERE ts BETWEEN '${new Date(laterFrom).toISOString()}' AND '${new Date(laterTo).toISOString()}'`,
    };
    const a = fingerprintPayload({
      targets: [targetA],
      visibleFromMs: visFrom,
      visibleToMs: visTo,
      options: baseOptions,
      seriesName: 'up',
    });
    const b = fingerprintPayload({
      targets: [targetB],
      visibleFromMs: laterFrom,
      visibleToMs: laterTo,
      options: baseOptions,
      seriesName: 'up',
    });
    expect(a).toEqual(b);
  });

  it('changes with expr, model, or train-range strings', () => {
    const target = { refId: 'A', datasource: { uid: 'prom' }, expr: 'up' };
    const base = fingerprintPayload({
      targets: [target],
      options: baseOptions,
      seriesName: 'up',
    });
    expect(
      fingerprintPayload({
        targets: [{ ...target, expr: 'up + 1' }],
        options: baseOptions,
        seriesName: 'up',
      })
    ).not.toEqual(base);
    expect(
      fingerprintPayload({
        targets: [target],
        options: { ...baseOptions, model: 'naive' },
        seriesName: 'up',
      })
    ).not.toEqual(base);
    expect(
      fingerprintPayload({
        targets: [target],
        options: { ...baseOptions, trainRange: { from: 'now-21d', to: 'now' } },
        seriesName: 'up',
      })
    ).not.toEqual(base);
  });

  it('matches overlay interpolated timestamps to alert time macros', () => {
    const fromMs = Date.UTC(2026, 7, 30, 16, 0, 0);
    const toMs = Date.UTC(2026, 7, 30, 22, 0, 0);
    const sql = (from: string, to: string) =>
      `SELECT __time, SUM("value") + 4.0 * SIN(TIMESTAMP_TO_MILLIS(__time) / 1009.0) AS "value" FROM minuteweek WHERE __time >= MILLIS_TO_TIMESTAMP(${from}) AND __time <= MILLIS_TO_TIMESTAMP(${to}) GROUP BY 1 ORDER BY 1`;
    const overlay = fingerprintPayload({
      targets: [
        {
          refId: 'A',
          key: 'Q-overlay',
          datasource: { type: 'grafadruid-druid-datasource', uid: 'druid', apiVersion: 'v1' },
          maxDataPoints: 15000,
          interval: '1m',
          intervalMs: 60000,
          builder: { queryType: 'sql', query: sql(String(fromMs), String(toMs)) },
          settings: { contextParameters: [], format: 'wide' },
        },
      ],
      visibleFromMs: fromMs,
      visibleToMs: toMs,
      options: baseOptions,
      seriesName: 'value',
    });
    const alert = fingerprintPayload({
      targets: [
        {
          refId: 'A',
          datasource: { uid: 'druid', type: 'grafadruid-druid-datasource' },
          builder: {
            queryType: 'sql',
            query: sql('${__from}', '${__to}'),
            aggregations: [{ name: 'value', type: 'doubleSum', fieldName: 'value' }],
            dataSource: { name: 'minuteweek', type: 'table' },
            intervals: { type: 'intervals', intervals: ['${__from:date:iso}/${__to:date:iso}'] },
          },
          settings: { format: 'wide', queryTimeout: 60 },
        },
      ],
      options: baseOptions,
      seriesName: 'value',
    });
    expect(alert).toEqual(overlay);
  });

  it('treats Auto empty trainRange the same as a missing trainRange', () => {
    const target = { refId: 'A', datasource: { uid: 'druid' }, builder: { queryType: 'sql', query: 'SELECT 1' } };
    const auto = fingerprintPayload({
      targets: [target],
      options: { ...baseOptions, trainRange: { from: '', to: '' } },
      seriesName: 'value',
    });
    const missing = fingerprintPayload({
      targets: [target],
      options: { ...baseOptions, trainRange: undefined as unknown as ForecastOptions['trainRange'] },
      seriesName: 'value',
    });
    expect(auto).toEqual(missing);
  });

  it('ignores SQL whitespace wrapping', () => {
    const a = fingerprintPayload({
      targets: [
        {
          datasource: { uid: 'druid' },
          builder: { queryType: 'sql', query: 'SELECT __time, value FROM minuteweek GROUP BY 1' },
        },
      ],
      options: baseOptions,
      seriesName: 'value',
    });
    const b = fingerprintPayload({
      targets: [
        {
          datasource: { uid: 'druid' },
          builder: {
            queryType: 'sql',
            query: 'SELECT __time, value\nFROM minuteweek\nGROUP BY 1',
          },
        },
      ],
      options: baseOptions,
      seriesName: 'value',
    });
    expect(a).toEqual(b);
  });
});

describe('cacheKey', () => {
  it('returns 64 hex chars', async () => {
    const key = await cacheKey({
      targets: [{ refId: 'A', datasource: { uid: 'prom' }, expr: 'up' }],
      options: baseOptions,
      seriesName: 'up',
    });
    expect(key).toMatch(/^[a-f0-9]{64}$/);
  });
});
