export const REASON_TRAIN_EMPTY = 'Training query returned no points';
export const REASON_INVALID_RANGE = 'Forecast range is inverted or invalid';
export const REASON_EMPTY_WINDOW = 'No forecast points in the requested range';
export const REASON_ALL_NAN = 'All forecast values in the window are missing';

export function hasDrawableValues(values: Array<number | null | undefined>): boolean {
  return values.some((v) => v != null && Number.isFinite(Number(v)));
}

export function reasonFromUnknown(err: unknown): string {
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
