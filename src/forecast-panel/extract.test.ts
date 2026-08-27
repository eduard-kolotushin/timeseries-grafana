import { FieldType, getFieldDisplayName, toDataFrame } from '@grafana/data';
import { extractSeries, pickTrainingPoints, trainingForFit } from './extract';

describe('extractSeries', () => {
  it('reads the time field and every numeric field', () => {
    const frame = toDataFrame({
      refId: 'A',
      fields: [
        { name: 'time', type: FieldType.time, values: [0, 1000, 2000] },
        { name: 'A-series', type: FieldType.number, values: [1, 2, 3] },
      ],
    });
    expect(extractSeries(frame)).toEqual([
      {
        name: 'A-series',
        times: [0, 1000, 2000],
        values: [1, 2, 3],
      },
    ]);
  });

  it('treats null numeric values as null', () => {
    const frame = toDataFrame({
      fields: [
        { name: 'time', type: FieldType.time, values: [0, 1000] },
        { name: 'v', type: FieldType.number, values: [1, null] },
      ],
    });
    expect(extractSeries(frame)[0]?.values).toEqual([1, null]);
  });

  it('returns no series without a time field', () => {
    const frame = toDataFrame({
      fields: [{ name: 'v', type: FieldType.number, values: [1] }],
    });
    expect(extractSeries(frame)).toEqual([]);
  });

  it('uses Grafana display names for labeled Prometheus fields', () => {
    const frame = toDataFrame({
      fields: [
        { name: 'Time', type: FieldType.time, values: [0, 1000] },
        {
          name: 'Value',
          type: FieldType.number,
          values: [1, 2],
          labels: { job: 'demo', instance: 'localhost:9090' },
        },
      ],
    });
    const series = extractSeries(frame);
    expect(series).toHaveLength(1);
    expect(series[0].name).toBe(getFieldDisplayName(frame.fields[1], frame, [frame]));
    expect(series[0].name).not.toBe('Value');
    expect(series[0].name).toMatch(/job/);
    expect(series[0].values).toEqual([1, 2]);
  });

  it('emits every numeric field on a wide Postgres frame', () => {
    const frame = toDataFrame({
      fields: [
        { name: 'time', type: FieldType.time, values: [0, 1000] },
        { name: 'cpu', type: FieldType.number, values: [1, 2] },
        { name: 'mem', type: FieldType.number, values: [3, 4] },
      ],
    });
    expect(extractSeries(frame)).toEqual([
      { name: 'cpu', times: [0, 1000], values: [1, 2] },
      { name: 'mem', times: [0, 1000], values: [3, 4] },
    ]);
  });

  it('skips logs and trace frames', () => {
    const logs = toDataFrame({
      meta: { preferredVisualisationType: 'logs' },
      fields: [
        { name: 'time', type: FieldType.time, values: [0] },
        { name: 'value', type: FieldType.number, values: [1] },
      ],
    });
    const traces = toDataFrame({
      meta: { preferredVisualisationType: 'trace' },
      fields: [
        { name: 'time', type: FieldType.time, values: [0] },
        { name: 'duration', type: FieldType.number, values: [1] },
      ],
    });
    expect(extractSeries(logs)).toEqual([]);
    expect(extractSeries(traces)).toEqual([]);
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

  it('does not fall back to the visible series when several trains miss', () => {
    expect(pickTrainingPoints(display, [other, { name: 'x', times: [], values: [] }])).toBeNull();
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

  it('returns null on name mismatch when several train series exist', () => {
    expect(
      trainingForFit(display, [
        { name: 'a', times: [0], values: [1] },
        { name: 'b', times: [0], values: [2] },
      ])
    ).toBeNull();
  });
});
