import { dateTime, FieldType, toDataFrame } from '@grafana/data';
import { FORECAST_DATASOURCE_TYPE } from '../forecast-datasource/types';
import { fingerprintPayload } from './cacheKey';
import { drawableFrames, framesQueryKey, isForecastTarget, LoadedFrames, metricTargets, splitPanelFrames } from './mixed';
import { ForecastOptions } from './types';

const metric = {
  refId: 'A',
  datasource: { uid: 'druid', type: 'grafadruid-druid-datasource' },
  rawSql: 'SELECT 1',
};
const forecast = {
  refId: 'B',
  datasource: { uid: 'fc', type: FORECAST_DATASOURCE_TYPE },
  kind: 'forecast' as const,
  sourceTargets: [metric],
  cacheKey: 'ab'.repeat(32),
};

describe('isForecastTarget', () => {
  it('matches the nested Forecast datasource type', () => {
    expect(isForecastTarget(forecast)).toBe(true);
    expect(isForecastTarget(metric)).toBe(false);
  });

  it('matches kind plus sourceTargets when type is missing', () => {
    expect(isForecastTarget({ refId: 'B', kind: 'forecast', sourceTargets: [metric] })).toBe(true);
    expect(isForecastTarget({ refId: 'A', kind: 'forecast' })).toBe(false);
  });
});

describe('metricTargets', () => {
  it('drops hidden and Forecast datasource rows', () => {
    expect(metricTargets([metric, forecast, { ...metric, refId: 'C', hide: true }])).toEqual([metric]);
  });
});

describe('splitPanelFrames', () => {
  it('keeps metric frames as history and Forecast refIds as datasource', () => {
    const historyFrame = toDataFrame({
      refId: 'A',
      fields: [
        { name: 'Time', type: FieldType.time, values: [1, 2] },
        { name: 'value', type: FieldType.number, values: [10, 20] },
      ],
    });
    const dsFrame = toDataFrame({
      refId: 'B',
      name: 'B',
      fields: [
        { name: 'Time', type: FieldType.time, values: [3, 4] },
        { name: 'Value', type: FieldType.number, values: [30, 30] },
      ],
    });
    const got = splitPanelFrames([historyFrame, dsFrame], [metric, forecast]);
    expect(got.history).toEqual([historyFrame]);
    expect(got.datasource).toEqual([dsFrame]);
  });

  it('classifies by frame name when refId is empty', () => {
    const dsFrame = toDataFrame({
      name: 'B',
      fields: [
        { name: 'Time', type: FieldType.time, values: [1] },
        { name: 'Value', type: FieldType.number, values: [2] },
      ],
    });
    dsFrame.refId = undefined;
    const got = splitPanelFrames([dsFrame], [forecast]);
    expect(got.datasource).toHaveLength(1);
    expect(got.history).toHaveLength(0);
  });
});

describe('framesQueryKey', () => {
  const base = {
    range: { from: dateTime(1_000), to: dateTime(2_000) },
    targets: [{ refId: 'A' }, { refId: 'B' }],
    intervalMs: 60_000,
  };

  it.each([
    ['the same query in a fresh request object', base, true],
    ['a moved range', { ...base, range: { from: dateTime(1_000), to: dateTime(3_000) } }, false],
    ['a dropped refId', { ...base, targets: [{ refId: 'A' }] }, false],
    ['a changed resolution', { ...base, intervalMs: 15_000 }, false],
  ])('%s → same key as the base query: %s', (_name, request, same) => {
    expect(framesQueryKey(request) === framesQueryKey(base)).toBe(same);
  });

  it('is empty without a request', () => {
    expect(framesQueryKey(undefined)).toBe('');
  });
});

describe('drawableFrames', () => {
  const history = [
    toDataFrame({
      refId: 'A',
      fields: [
        { name: 'Time', type: FieldType.time, values: [2_000, 3_000] },
        { name: 'value', type: FieldType.number, values: [10, 20] },
      ],
    }),
  ];
  const plotted = [
    ...history,
    toDataFrame({
      refId: 'value',
      fields: [
        { name: 'Time', type: FieldType.time, values: [4_000, 5_000] },
        { name: 'value', type: FieldType.number, values: [30, 40] },
      ],
    }),
  ];
  const currentKey = framesQueryKey({
    range: { from: dateTime(2_000), to: dateTime(4_000) },
    targets: [{ refId: 'A' }],
  });
  const previousKey = framesQueryKey({
    range: { from: dateTime(1_000), to: dateTime(2_000) },
    targets: [{ refId: 'A' }],
  });

  it.each<[string, LoadedFrames | undefined, string]>([
    ['the load that answers the current query', { key: currentKey, frames: plotted }, 'load'],
    ['a load left over from the previous range', { key: previousKey, frames: plotted }, 'history'],
    ['a load that never finished', undefined, 'history'],
    ['a load that drew nothing', { key: currentKey, frames: [] }, 'history'],
  ])('%s → drawn from the %s', (_name, load, source) => {
    expect(drawableFrames(load, currentKey, history)).toBe(source === 'load' ? plotted : history);
  });
});

describe('metricTargets cache fingerprint', () => {
  const options: ForecastOptions = {
    model: 'baseline',
    alpha: 0.8,
    beta: 0.2,
    period: 7,
    season: 'minute-week',
    calendar: '',
    showInterval: true,
    interval: 0.95,
    trainRange: { from: '', to: '' },
    forecastRange: { from: '', to: '' },
  };

  it('does not change when a Forecast datasource query is added', () => {
    const metricOnly = fingerprintPayload({
      targets: [metric],
      options,
      seriesName: 'value',
    });
    const mixed = fingerprintPayload({
      targets: metricTargets([metric, forecast]),
      options,
      seriesName: 'value',
    });
    expect(mixed).toEqual(metricOnly);
    expect(
      fingerprintPayload({
        targets: [metric, forecast],
        options,
        seriesName: 'value',
      })
    ).not.toEqual(metricOnly);
  });
});
