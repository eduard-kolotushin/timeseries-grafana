import { applyLookbackRange, autoForecastHorizon, autoLookback, forecastLevel, isoUtc, isInvalidForecastWindow, resolveForecastWindow, resolveLookbackMs, resolveTrainWindow, trainMaxDataPoints, trainStepInterval, trainStepMs } from './lookback';

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

describe('resolveTrainWindow', () => {
  const panelTo = Date.UTC(2026, 7, 19, 12, 0, 0);

  it('uses Auto ending at panel to when trainRange is unset', () => {
    expect(resolveTrainWindow({ model: 'holt' }, panelTo)).toEqual({
      fromMs: panelTo - 7 * day,
      toMs: panelTo,
    });
  });

  it('keeps a legacy duration lookback when trainRange was never saved', () => {
    expect(resolveTrainWindow({ model: 'holt', lookback: '30d' }, panelTo)).toEqual({
      fromMs: panelTo - 30 * day,
      toMs: panelTo,
    });
  });

  it('ignores legacy lookback when the picker is cleared to Auto', () => {
    expect(resolveTrainWindow({ model: 'holt', lookback: '30d', trainRange: { from: '', to: '' } }, panelTo)).toEqual({
      fromMs: panelTo - 7 * day,
      toMs: panelTo,
    });
  });

  it('uses an absolute from/to range', () => {
    const from = '2026-07-01T00:00:00.000Z';
    const to = '2026-08-01T00:00:00.000Z';
    expect(resolveTrainWindow({ model: 'holt', trainRange: { from, to } }, panelTo, 'utc')).toEqual({
      fromMs: Date.parse(from),
      toMs: Date.parse(to),
    });
  });

  it('parses a relative Grafana range', () => {
    const { fromMs, toMs } = resolveTrainWindow(
      { model: 'holt', trainRange: { from: 'now-7d', to: 'now' } },
      panelTo,
      'utc'
    );
    expect(Math.abs(toMs - fromMs - 7 * day)).toBeLessThan(2);
  });

  it('falls back to Auto when from is not before to', () => {
    expect(
      resolveTrainWindow({ model: 'holt', trainRange: { from: 'now', to: 'now-1h' } }, panelTo, 'utc')
    ).toEqual({
      fromMs: panelTo - 7 * day,
      toMs: panelTo,
    });
  });
});

describe('trainStepMs', () => {
  it.each([
    ['baseline', 'minute-week', 15_000, 60_000],
    ['baseline', 'hour', 15_000, 3_600_000],
    ['baseline', 'week', 15_000, 3_600_000],
    ['baseline', 'day', 15_000, 86_400_000],
    ['holt', undefined, 15_000, 60_000],
    ['holt', undefined, 300_000, 300_000],
    ['ses', undefined, 0, 60_000],
  ] as const)('%s %s interval=%s → %s', (model, season, intervalMs, want) => {
    expect(trainStepMs(model, season, intervalMs)).toBe(want);
  });
});

describe('trainStepInterval', () => {
  it.each([
    [60_000, '1m'],
    [3_600_000, '1h'],
    [86_400_000, '1d'],
    [120_000, '2m'],
  ] as const)('%s → %s', (ms, want) => {
    expect(trainStepInterval(ms)).toBe(want);
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

const hour = 60 * 60 * 1000;

describe('autoForecastHorizon', () => {
  it.each([
    ['baseline', 'minute-week', '6h'],
    ['baseline', 'week', '6h'],
    ['baseline', 'hour', '24h'],
    ['baseline', 'day', '7d'],
    ['seasonal', undefined, '24h'],
    ['holt', undefined, '6h'],
    ['ses', undefined, '6h'],
    ['naive', undefined, '6h'],
    ['mean', undefined, '6h'],
    ['drift', undefined, '6h'],
  ] as const)('%s %s → %s', (model, season, want) => {
    expect(autoForecastHorizon(model, season)).toBe(want);
  });
});

describe('resolveForecastWindow', () => {
  const nowMs = Date.UTC(2026, 7, 23, 12, 0, 0);

  it('uses Auto from dashboard now through now + model duration', () => {
    expect(resolveForecastWindow({ model: 'holt' }, nowMs)).toEqual({
      fromMs: nowMs,
      toMs: nowMs + 6 * hour,
    });
    expect(resolveForecastWindow({ model: 'baseline', season: 'hour' }, nowMs)).toEqual({
      fromMs: nowMs,
      toMs: nowMs + 24 * hour,
    });
    expect(resolveForecastWindow({ model: 'baseline', season: 'day' }, nowMs)).toEqual({
      fromMs: nowMs,
      toMs: nowMs + 7 * day,
    });
  });

  it('ignores a leftover horizon and still uses Auto when forecastRange is unset', () => {
    expect(resolveForecastWindow({ model: 'holt' }, nowMs)).toEqual({
      fromMs: nowMs,
      toMs: nowMs + 6 * hour,
    });
  });

  it('uses an absolute from/to range', () => {
    const from = '2026-08-23T12:00:00.000Z';
    const to = '2026-08-23T18:00:00.000Z';
    expect(resolveForecastWindow({ model: 'holt', forecastRange: { from, to } }, nowMs, 'utc')).toEqual({
      fromMs: Date.parse(from),
      toMs: Date.parse(to),
    });
  });

  it('marks an inverted range invalid instead of falling back to Auto', () => {
    const w = resolveForecastWindow(
      { model: 'holt', forecastRange: { from: 'now', to: 'now-1h' } },
      nowMs,
      'utc'
    );
    expect(isInvalidForecastWindow(w)).toBe(true);
  });
});
