export const REASON_TRAIN_EMPTY = 'Training query returned no points';
export const REASON_INVALID_RANGE = 'Forecast range is inverted or invalid';
export const REASON_EMPTY_WINDOW = 'No forecast points in the requested range';
export const REASON_ALL_NAN = 'All forecast values in the window are missing';
export const REASON_UNSUPPORTED_PROM_INSTANT = 'Prometheus instant queries cannot train a forecast';
export const REASON_UNSUPPORTED_OS_QUERY = 'OpenSearch logs, raw, and traces queries cannot train a forecast';
export const REASON_UNSUPPORTED_PG_FORMAT = 'Postgres table and EXPLAIN queries cannot train a forecast';
export const REASON_TRAIN_TOO_LONG = 'Training series exceeds the 100000 point limit';
export const REASON_OVERSIZE = 'Forecast request is too large';
export const REASON_BUSY = 'Forecast backend is busy';
export const REASON_BACKEND = 'Forecast backend error';

export function hasDrawableValues(values: Array<number | null | undefined>): boolean {
  return values.some((v) => v != null && Number.isFinite(Number(v)));
}

export function httpStatusFromUnknown(err: unknown): number | undefined {
  if (!err || typeof err !== 'object') {
    return undefined;
  }
  const status = (err as { status?: unknown }).status;
  return typeof status === 'number' ? status : undefined;
}

export function isAbortError(err: unknown): boolean {
  if (err instanceof DOMException && err.name === 'AbortError') {
    return true;
  }
  return Boolean(err && typeof err === 'object' && (err as { name?: string }).name === 'AbortError');
}

export function reasonFromUnknown(err: unknown): string {
  const status = httpStatusFromUnknown(err);
  if (status === 413) {
    return REASON_OVERSIZE;
  }
  if (status === 429) {
    return REASON_BUSY;
  }
  if (status != null && status >= 500) {
    return REASON_BACKEND;
  }
  if (typeof err === 'string' && err.trim()) {
    return err.trim();
  }
  if (err && typeof err === 'object') {
    const obj = err as Record<string, unknown>;
    if (typeof obj.data === 'string' && obj.data.trim()) {
      return obj.data.trim();
    }
    if (obj.data && typeof obj.data === 'object') {
      const data = obj.data as Record<string, unknown>;
      if (typeof data.message === 'string' && data.message.trim()) {
        return data.message.trim();
      }
    }
    if (typeof obj.message === 'string' && obj.message.trim() && obj.message !== 'FetchError') {
      return obj.message.trim();
    }
  }
  if (err instanceof Error && err.message) {
    return err.message;
  }
  return String(err);
}
