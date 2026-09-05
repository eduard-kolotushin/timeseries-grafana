import { MutableDataFrame, FieldType } from '@grafana/data';
import { MAX_TRAIN_POINTS } from './lookback';
import { loadOverlayForecasts } from './overlayLoad';
import { REASON_BUSY, REASON_OVERSIZE, REASON_TRAIN_EMPTY, REASON_TRAIN_TOO_LONG } from './reasons';
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

  it('does not POST a train body longer than MAX_TRAIN_POINTS', async () => {
    const n = MAX_TRAIN_POINTS + 1;
    const long = new MutableDataFrame();
    long.addField({ name: 'Time', type: FieldType.time, values: Array.from({ length: n }, (_, i) => i) });
    long.addField({ name: 'up', type: FieldType.number, values: Array.from({ length: n }, () => 1) });
    const queryTrain = jest.fn(async () => ({ frames: [long] }));
    const post = jest.fn<Promise<ForecastResponse>, [Record<string, unknown>]>(async () => ({
      needTrain: true,
    }));
    const got = await loadOverlayForecasts({
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
    expect(got.error).toBe(REASON_TRAIN_TOO_LONG);
    expect(post).not.toHaveBeenCalled();
  });

  it('maps 429 and 413 and does not POST the next series', async () => {
    const post = jest.fn<Promise<ForecastResponse>, [Record<string, unknown>]>().mockRejectedValueOnce({ status: 429 });
    const got = await loadOverlayForecasts({
      visible: [
        { name: 'a', times: [1], values: [1] },
        { name: 'b', times: [1], values: [1] },
      ],
      fromMs: 3,
      toMs: 4,
      level: 0,
      retrain: false,
      fitBody: { model: 'naive' },
      cacheKeyFor: async () => 'aa'.repeat(32),
      queryTrain: jest.fn(),
      post,
    });
    expect(got.error).toBe(REASON_BUSY);
    expect(post).toHaveBeenCalledTimes(1);
    post.mockReset();
    post.mockRejectedValueOnce({ status: 413 });
    const oversize = await loadOverlayForecasts({
      visible,
      fromMs: 3,
      toMs: 4,
      level: 0,
      retrain: false,
      fitBody: { model: 'naive' },
      cacheKeyFor: async () => 'aa'.repeat(32),
      queryTrain: jest.fn(),
      post,
    });
    expect(oversize.error).toBe(REASON_OVERSIZE);
  });

  it('stops further POSTs when the load is aborted', async () => {
    const ac = new AbortController();
    const post = jest.fn<Promise<ForecastResponse>, [Record<string, unknown>]>(async () => {
      ac.abort();
      return { times: [3], values: [4], cached: true };
    });
    const got = await loadOverlayForecasts({
      visible: [
        { name: 'a', times: [1], values: [1] },
        { name: 'b', times: [1], values: [1] },
      ],
      fromMs: 3,
      toMs: 4,
      level: 0,
      retrain: false,
      fitBody: { model: 'naive' },
      cacheKeyFor: async () => 'aa'.repeat(32),
      queryTrain: jest.fn(),
      post,
      signal: ac.signal,
    });
    expect(post).toHaveBeenCalledTimes(1);
    expect(got.error).toBeNull();
    expect(got.forecasts).toHaveLength(1);
  });
});
