import { DataFrame, FieldType } from '@grafana/data';

export type SeriesPoints = {
  name: string;
  times: number[];
  values: Array<number | null>;
};

export function extractSeries(frame: DataFrame): SeriesPoints | null {
  const timeField = frame.fields.find((f) => f.type === FieldType.time);
  const valueField = frame.fields.find((f) => f.type === FieldType.number);
  if (!timeField || !valueField) {
    return null;
  }
  const times: number[] = [];
  const values: Array<number | null> = [];
  const n = timeField.values.length;
  for (let i = 0; i < n; i++) {
    const t = Number(timeField.values[i]);
    const v = valueField.values[i];
    if (!Number.isFinite(t)) {
      continue;
    }
    times.push(t);
    if (v == null || (typeof v === 'number' && Number.isNaN(v))) {
      values.push(null);
    } else {
      values.push(Number(v));
    }
  }
  if (times.length === 0) {
    return null;
  }
  return {
    name: valueField.name || frame.name || frame.refId || 'series',
    times,
    values,
  };
}
