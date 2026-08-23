import { FieldType, toDataFrame } from '@grafana/data';
import { extractSeries, pickTrainingPoints, trainingForFit } from './extract';

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

describe('pickTrainingPoints', () => {
  const display = { name: 'views', times: [1], values: [1] };
  const trained = { name: 'views', times: [0, 1], values: [0, 1] };
  const other = { name: 'other', times: [0], values: [9] };

  it('matches by name', () => {
    expect(pickTrainingPoints(display, [other, trained])).toBe(trained);
  });

  it('uses the only trained series when names differ', () => {
    expect(pickTrainingPoints(display, [other])).toBe(other);
  });

  it('falls back to display when nothing matches', () => {
    expect(pickTrainingPoints(display, [other, { name: 'x', times: [], values: [] }])).toBe(display);
  });
});

describe('trainingForFit', () => {
  const display = { name: 'views', times: [1], values: [1] };
  const trained = { name: 'views', times: [0, 1], values: [0, 1] };

  it('returns null when the training query is empty (no silent fallback)', () => {
    expect(trainingForFit(display, [])).toBeNull();
  });

  it('uses matched training points when the query returned data', () => {
    expect(trainingForFit(display, [trained])).toBe(trained);
  });
});
