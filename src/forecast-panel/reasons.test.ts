import {
  REASON_ALL_NAN,
  REASON_EMPTY_WINDOW,
  REASON_INVALID_RANGE,
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

  it('keeps empty-window and all-NaN reasons distinct', () => {
    expect(REASON_EMPTY_WINDOW).not.toBe(REASON_ALL_NAN);
  });
});
