import {
  REASON_BACKEND,
  REASON_BUSY,
  REASON_INVALID_RANGE,
  REASON_OVERSIZE,
  REASON_TRAIN_EMPTY,
  hasDrawableValues,
  reasonFromUnknown,
} from './reasons';

describe('hasDrawableValues', () => {
  it('is false for an empty list or all missing values', () => {
    expect(hasDrawableValues([])).toBe(false);
    expect(hasDrawableValues([null, NaN, undefined])).toBe(false);
  });

  it('is true when any value is finite', () => {
    expect(hasDrawableValues([null, 1.5])).toBe(true);
  });
});

describe('reasonFromUnknown', () => {
  it('reads a Grafana FetchError data string', () => {
    expect(reasonFromUnknown({ data: 'forecast: no forecast points in the requested range' })).toBe(
      'forecast: no forecast points in the requested range'
    );
  });

  it('reads a nested message', () => {
    expect(reasonFromUnknown({ data: { message: REASON_INVALID_RANGE } })).toBe(REASON_INVALID_RANGE);
  });

  it('uses Error.message', () => {
    expect(reasonFromUnknown(new Error(REASON_TRAIN_EMPTY))).toBe(REASON_TRAIN_EMPTY);
  });

  it('maps 413, 429, and 5xx to load reasons', () => {
    expect(reasonFromUnknown({ status: 413, data: 'ignored' })).toBe(REASON_OVERSIZE);
    expect(reasonFromUnknown({ status: 429, data: 'ignored' })).toBe(REASON_BUSY);
    expect(reasonFromUnknown({ status: 500, data: 'ignored' })).toBe(REASON_BACKEND);
  });
});
