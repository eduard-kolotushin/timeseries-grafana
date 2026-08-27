import { MutableDataFrame, FieldType } from '@grafana/data';
import { loadOverlayForecasts } from './overlayLoad';
import { REASON_TRAIN_EMPTY } from './reasons';
import { ForecastResponse } from './types';

const visible = [{ name: 'up', times: [1, 2], values: [1, 2] }];

function frame(): MutableDataFrame {
  const f = new MutableDataFrame();
  f.addField({ name: 'Time', type: FieldType.time, values: [1, 2] });
  f.addField({ name: 'up', type: FieldType.number, values: [10, 20] });
  return f;
}

describe('loadOverlayForecasts', () => {
  it('does not query train when the probe is a cache hit', async () => {
    const queryTrain = jest.fn();
    const post = jest.fn<Promise<ForecastResponse>, [Record<string, unknown>]>(async () => ({
      times: [3],
      values: [4],
      cached: true,
    }));
    const got = await loadOverlayForecasts({
      visible,
      fromMs: 3,
      toMs: 4,
      level: 0,
      retrain: false,
      fitBody: { model: 'naive' },
      cacheKeyFor: async () => 'aa'.repeat(32),
      queryTrain,
      post,
    });
    expect(queryTrain).not.toHaveBeenCalled();
    expect(got.usedSaved).toBe(true);
    expect(got.forecasts).toHaveLength(1);
    expect(post).toHaveBeenCalledTimes(1);
    expect(post.mock.calls[0][0].times).toBeUndefined();
  });

  it('queries train once on needTrain', async () => {
    const queryTrain = jest.fn(async () => ({ frames: [frame()] }));
    const post = jest
      .fn<Promise<ForecastResponse>, [Record<string, unknown>]>()
      .mockResolvedValueOnce({ needTrain: true })
      .mockResolvedValueOnce({ times: [3], values: [30] });
    const got = await loadOverlayForecasts({
      visible,
      fromMs: 3,
      toMs: 4,
      level: 0,
      retrain: false,
      fitBody: { model: 'naive' },
      cacheKeyFor: async () => 'aa'.repeat(32),
      queryTrain,
      post,
    });
    expect(queryTrain).toHaveBeenCalledTimes(1);
    expect(got.usedSaved).toBe(false);
    expect(got.forecasts[0].values[0]).toBe(30);
    expect(post.mock.calls[1][0].times).toEqual([1, 2]);
  });

  it('skips the probe and trains when retrain is set', async () => {
    const queryTrain = jest.fn(async () => ({ frames: [frame()] }));
    const post = jest.fn<Promise<ForecastResponse>, [Record<string, unknown>]>(async () => ({
      times: [3],
      values: [30],
    }));
    await loadOverlayForecasts({
      visible,
      fromMs: 3,
      toMs: 4,
      level: 0,
      retrain: true,
      fitBody: { model: 'naive' },
      cacheKeyFor: async () => 'aa'.repeat(32),
      queryTrain,
      post,
    });
    expect(queryTrain).toHaveBeenCalledTimes(1);
    expect(post).toHaveBeenCalledTimes(1);
    expect(post.mock.calls[0][0].times).toEqual([1, 2]);
  });

  it('returns train empty without fitting visible points', async () => {
    const queryTrain = jest.fn(async () => ({ frames: [] }));
    const post = jest.fn<Promise<ForecastResponse>, [Record<string, unknown>]>(async () => ({
      needTrain: true,
    }));
    const got = await loadOverlayForecasts({
      visible,
      fromMs: 3,
      toMs: 4,
      level: 0,
      retrain: false,
      fitBody: { model: 'naive' },
      cacheKeyFor: async () => 'aa'.repeat(32),
      queryTrain,
      post,
    });
    expect(got.error).toBe(REASON_TRAIN_EMPTY);
    expect(got.forecasts).toHaveLength(0);
    expect(post).toHaveBeenCalledTimes(1);
  });
});
