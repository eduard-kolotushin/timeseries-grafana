import { applyLookbackRange, autoLookback, forecastLevel, isoUtc, resolveLookbackMs, trainMaxDataPoints } from './lookback';

const day = 24 * 60 * 60 * 1000;

describe('autoLookback', () => {
  it.each([
    ['baseline', 'minute-week', '21d'],
    ['baseline', 'week', '21d'],
    ['baseline', 'hour', '14d'],
    ['baseline', 'day', '56d'],
    ['seasonal', undefined, '14d'],
    ['holt', undefined, '7d'],
    ['ses', undefined, '7d'],
    ['naive', undefined, '7d'],
    ['mean', undefined, '7d'],
    ['drift', undefined, '7d'],
  ] as const)('%s %s → %s', (model, season, want) => {
    expect(autoLookback(model, season)).toBe(want);
  });
});

describe('resolveLookbackMs', () => {
  it.each([
    { lookback: 'auto', model: 'holt' as const, season: undefined, want: 7 * day },
    { lookback: '', model: 'holt' as const, season: undefined, want: 7 * day },
    { lookback: '30d', model: 'holt' as const, season: undefined, want: 30 * day },
    { lookback: 'auto', model: 'baseline' as const, season: 'minute-week' as const, want: 21 * day },
    { lookback: '15d', model: 'holt' as const, season: undefined, want: 15 * day },
    { lookback: '15', model: 'holt' as const, season: undefined, want: 15 * day },
    { lookback: '48h', model: 'holt' as const, season: undefined, want: 2 * day },
  ])('$model lookback=$lookback → $want ms', ({ lookback, model, season, want }) => {
    expect(resolveLookbackMs({ model, season, lookback })).toBe(want);
  });

  it('falls back to Auto when the override is not a duration', () => {
    expect(resolveLookbackMs({ model: 'holt', lookback: 'nope' })).toBe(7 * day);
  });
});

describe('trainMaxDataPoints', () => {
  it('sizes to lookback / interval and caps at 100000', () => {
    expect(trainMaxDataPoints(21 * day, 60_000)).toBe(21 * 24 * 60 + 1);
    expect(trainMaxDataPoints(21 * day, 1)).toBe(100_000);
  });

  it('uses 1m when intervalMs is missing', () => {
    expect(trainMaxDataPoints(day, 0)).toBe(24 * 60 + 1);
  });
});

describe('forecastLevel', () => {
  it.each([
    { showInterval: true, interval: 0.95, want: 0.95 },
    { showInterval: undefined, interval: 0.8, want: 0.8 },
    { showInterval: undefined, interval: undefined, want: 0.95 },
    { showInterval: false, interval: 0.95, want: 0 },
    { showInterval: true, interval: 0, want: 0 },
  ])('$showInterval / $interval → $want', ({ showInterval, interval, want }) => {
    expect(forecastLevel({ showInterval, interval })).toBe(want);
  });
});

describe('applyLookbackRange', () => {
  const visFrom = Date.UTC(2026, 7, 18, 10, 0, 0);
  const visTo = Date.UTC(2026, 7, 18, 16, 0, 0);
  const trainFrom = visTo - 21 * day;
  const trainTo = visTo;

  it('substitutes Druid interval templates', () => {
    const [got] = applyLookbackRange(
      [
        {
          builder: {
            intervals: { type: 'intervals', intervals: ['${__from:date:iso}/${__to:date:iso}'] },
          },
        },
      ],
      trainFrom,
      trainTo
    );
    expect(got.builder.intervals.intervals).toEqual([`${isoUtc(trainFrom)}/${isoUtc(trainTo)}`]);
  });

  it('rewrites already-interpolated dashboard intervals', () => {
    const [got] = applyLookbackRange(
      [
        {
          builder: {
            intervals: {
              type: 'intervals',
              intervals: [`${isoUtc(visFrom)}/${isoUtc(visTo)}`],
            },
          },
        },
      ],
      trainFrom,
      trainTo,
      visFrom,
      visTo
    );
    expect(got.builder.intervals.intervals).toEqual([`${isoUtc(trainFrom)}/${isoUtc(trainTo)}`]);
  });

  it('rewrites SQL millis placeholders', () => {
    const [got] = applyLookbackRange(
      [
        {
          builder: {
            queryType: 'sql',
            query: 'SELECT __time FROM t WHERE __time >= MILLIS_TO_TIMESTAMP(${__from}) AND __time <= MILLIS_TO_TIMESTAMP(${__to})',
          },
        },
      ],
      trainFrom,
      trainTo
    );
    expect(got.builder.query).toContain(`MILLIS_TO_TIMESTAMP(${trainFrom})`);
    expect(got.builder.query).toContain(`MILLIS_TO_TIMESTAMP(${trainTo})`);
    expect(got.builder.query).not.toContain('${__from}');
  });
});
