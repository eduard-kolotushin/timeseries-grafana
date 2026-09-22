import { DataFrame, FieldConfig, FieldType, getFieldDisplayName, QueryResultMeta } from '@grafana/data';

export type SeriesPoints = {
  name: string;
  times: number[];
  values: Array<number | null>;
  /** The source field's own config (unit, decimals, min/max, links), so a rebuild of the frame keeps it. */
  config?: FieldConfig;
  /** The source frame's meta, likewise carried over by a rebuild. */
  meta?: QueryResultMeta;
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
    // The datasource's own config (unit, decimals, min/max, links) and the frame meta ride
    // along, so a panel rebuilding this series into a frame does not lose them. Left off
    // when the source set nothing.
    const config = valueField.config && Object.keys(valueField.config).length > 0 ? valueField.config : undefined;
    out.push({
      name: getFieldDisplayName(valueField, frame, frames),
      times,
      values,
      config,
      meta: frame.meta,
    });
  }
  return out;
}

/**
 * A frame for one series, rebuilt from the points a panel has to plot. The datasource's
 * own field config and the frame's meta ride along: the overlay panel rebuilds history to
 * hang the forecast off it, and flattening a series to name/times/values drops the unit,
 * decimals, min/max, links and the frame meta the datasource supplied.
 */
export function historyFrame(points: SeriesPoints): DataFrame {
  return {
    name: points.name,
    refId: points.name,
    meta: points.meta,
    length: points.times.length,
    fields: [
      { name: 'Time', type: FieldType.time, values: points.times, config: {} },
      {
        name: points.name,
        type: FieldType.number,
        values: points.values,
        config: { ...points.config, displayName: points.name },
      },
    ],
  };
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
