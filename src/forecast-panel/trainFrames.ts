import {
  DataFrame,
  FieldType,
  ensureTimeField,
  getProcessedDataFrames,
  type DataQueryResponseData,
} from '@grafana/data';

/**
 * A datasource's reply as frames the training extractor can read.
 *
 * `DataQueryResponse.data` is `DataQueryResponseData[]`, a type that admits the pre-DataFrame shapes
 * (`LegacyResponseData` is `TimeSeries | TableData | any`), while the frames the overlay fits from have to
 * be real `DataFrame`s. A frontend datasource answers with whatever its own `query()` returned:
 * `sbt-datasource-abyss` sends a legacy table object (`{columns, rows, type:'table'}`) for a
 * `format: "table"` target, and an old plugin may send graphite `{datapoints}`.
 *
 * `getProcessedDataFrames` is Grafana's own panel-pipeline converter for those shapes — the one the visible
 * series goes through — and it types an untyped table's columns. It leaves a column already marked
 * `type: 'time'` untouched, though, its local date strings (`"2026-10-07 18:00:00"`) still in `values`, and
 * `extractSeries` reads those as `Number('2026-10-07 18:00:00')` → NaN → no series at all. So a `time`
 * field whose values are not numbers yet goes through `ensureTimeField`, Grafana's own string→epoch-ms
 * conversion. `ensureTimeField` is the identity for a numeric time field, so a frame is only rebuilt when
 * one of its fields changed — and copied rather than written into, because `getProcessedDataFrames` hands
 * back the caller's own object when the reply already was a `DataFrame`.
 */
export function toTrainingFrames(data: DataQueryResponseData[]): DataFrame[] {
  const frames: DataFrame[] = [];
  if (!Array.isArray(data)) {
    return frames;
  }
  for (const item of data) {
    try {
      // `toDataFrame` throws `Unsupported data format` on a shape Grafana cannot read. Dropping the
      // item keeps the other datasources' frames; a reply that converts to nothing already has an
      // answer on the caller's empty-frames path. The string→epoch-ms conversion sits inside the
      // same guard: one malformed field drops the item instead of throwing out of the function.
      for (const frame of getProcessedDataFrames([item])) {
        let changed = false;
        const fields = frame.fields.map((field) => {
          if (field.type !== FieldType.time || field.values.every((v) => v == null || typeof v === 'number')) {
            return field;
          }
          changed = true;
          return ensureTimeField(field);
        });
        frames.push(changed ? { ...frame, fields } : frame);
      }
    } catch {
      continue;
    }
  }
  return frames;
}
