import { DataFrame, FieldType, getFieldDisplayName } from '@grafana/data';

export type SeriesPoints = {
  name: string;
  times: number[];
  values: Array<number | null>;
};

/** Every numeric field on a timeseries frame. Skips logs/trace frames. */
export function extractSeries(frame: DataFrame, allFrames?: DataFrame[]): SeriesPoints[] {
  if (isNonTimeseriesFrame(frame)) {
    return [];
  }
  const timeField = frame.fields.find((f) => f.type === FieldType.time);
  if (!timeField) {
    return [];
  }
  const frames = allFrames ?? [frame];
  const out: SeriesPoints[] = [];
  for (const valueField of frame.fields) {
    if (valueField.type !== FieldType.number) {
      continue;
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
      continue;
    }
    out.push({
      name: getFieldDisplayName(valueField, frame, frames),
      times,
      values,
    });
  }
  return out;
}

function isNonTimeseriesFrame(frame: DataFrame): boolean {
  const meta = frame.meta;
  if (!meta) {
    return false;
  }
  const vis = meta.preferredVisualisationType;
  if (vis === 'logs' || vis === 'trace' || vis === 'nodeGraph') {
    return true;
  }
  const t = String(meta.type ?? '');
  return t === 'log-lines' || t === 'heatmap-cells' || t === 'heatmap-rows' || t === 'directory-listing';
}

export function pickTrainingPoints(display: SeriesPoints, trained: SeriesPoints[]): SeriesPoints | null {
  const byName = trained.find((t) => t.name === display.name);
  if (byName) {
    return byName;
  }
  if (trained.length === 1) {
    return trained[0];
  }
  return null;
}

/** Training points to POST. Empty train must not fall back to the visible series. */
export function trainingForFit(display: SeriesPoints, trained: SeriesPoints[]): SeriesPoints | null {
  if (trained.length === 0) {
    return null;
  }
  return pickTrainingPoints(display, trained);
}
