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
