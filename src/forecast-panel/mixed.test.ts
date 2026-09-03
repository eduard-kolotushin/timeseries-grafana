import { FieldType, toDataFrame } from '@grafana/data';
import { FORECAST_DATASOURCE_TYPE } from '../forecast-datasource/types';
import { fingerprintPayload } from './cacheKey';
import { isForecastTarget, metricTargets, splitPanelFrames } from './mixed';
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
