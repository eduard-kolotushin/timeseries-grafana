import { FieldType, toDataFrame } from '@grafana/data';
import { extractSeries } from './extract';

describe('extractSeries', () => {
  it('reads the time field and first numeric field', () => {
    const frame = toDataFrame({
      refId: 'A',
      fields: [
        { name: 'time', type: FieldType.time, values: [0, 1000, 2000] },
        { name: 'A-series', type: FieldType.number, values: [1, 2, 3] },
      ],
    });
    expect(extractSeries(frame)).toEqual({
      name: 'A-series',
      times: [0, 1000, 2000],
      values: [1, 2, 3],
    });
  });

  it('treats null numeric values as null', () => {
    const frame = toDataFrame({
      fields: [
        { name: 'time', type: FieldType.time, values: [0, 1000] },
        { name: 'v', type: FieldType.number, values: [1, null] },
      ],
    });
    expect(extractSeries(frame)?.values).toEqual([1, null]);
  });

  it('returns null without a time field', () => {
    const frame = toDataFrame({
      fields: [{ name: 'v', type: FieldType.number, values: [1] }],
    });
    expect(extractSeries(frame)).toBeNull();
  });
});
