import { DataFrame } from '@grafana/data';
import { extractSeries, SeriesPoints, trainingForFit } from './extract';
import { MAX_TRAIN_POINTS } from './lookback';
import {
  REASON_ALL_NAN,
  REASON_EMPTY_WINDOW,
  REASON_TRAIN_EMPTY,
  REASON_TRAIN_TOO_LONG,
  hasDrawableValues,
  httpStatusFromUnknown,
  isAbortError,
  reasonFromUnknown,
} from './reasons';
import { TrainQueryResult } from './trainQuery';
import { ForecastResponse } from './types';

export type OverlayForecast = {
  name: string;
  times: number[];
  values: number[];
  lower?: Array<number | null>;
  upper?: Array<number | null>;
};

export type OverlayPostBody = Record<string, unknown>;

export type OverlayLoadArgs = {
  visible: SeriesPoints[];
  fromMs: number;
  toMs: number;
  level: number;
  retrain: boolean;
  fitBody: OverlayPostBody;
  cacheKeyFor: (seriesName: string) => Promise<string>;
  queryTrain: () => Promise<TrainQueryResult>;
  post: (body: OverlayPostBody) => Promise<ForecastResponse>;
  signal?: AbortSignal;
};

export type OverlayLoadResult = {
  forecasts: OverlayForecast[];
  error: string | null;
  usedSaved: boolean;
};

export async function loadOverlayForecasts(args: OverlayLoadArgs): Promise<OverlayLoadResult> {
  const forecasts: OverlayForecast[] = [];
  const need: SeriesPoints[] = [];
  let usedSaved = false;
  let overlayError: string | null = null;

  if (!args.retrain) {
    for (const points of args.visible) {
      try {
        throwIfAborted(args.signal);
        const key = await args.cacheKeyFor(points.name);
        const resp = await args.post({
          ...args.fitBody,
          cacheKey: key,
          from: args.fromMs,
          to: args.toMs,
          level: args.level,
        });
        if (resp.needTrain) {
          need.push(points);
          continue;
        }
        const drawn = pushForecast(forecasts, points.name, resp);
        if (drawn.ok) {
          usedSaved = usedSaved || Boolean(resp.cached);
        } else {
          overlayError = overlayError ?? drawn.reason;
        }
      } catch (e) {
        if (isAbortError(e)) {
          return { forecasts, error: overlayError, usedSaved };
        }
        overlayError = overlayError ?? reasonFromUnknown(e);
        if (isLoadLimitStatus(httpStatusFromUnknown(e))) {
          return { forecasts, error: overlayError, usedSaved };
        }
      }
    }
  } else {
    need.push(...args.visible);
  }

  if (need.length === 0) {
    return { forecasts, error: overlayError, usedSaved };
  }

  let train: TrainQueryResult;
  try {
    throwIfAborted(args.signal);
    train = await args.queryTrain();
  } catch (e) {
    if (isAbortError(e)) {
      return { forecasts, error: overlayError, usedSaved };
    }
    return { forecasts, error: overlayError ?? reasonFromUnknown(e), usedSaved };
  }
  if (train.reason && !train.frames?.length) {
    return { forecasts, error: overlayError ?? train.reason, usedSaved };
  }
  const trained = (train.frames ?? []).flatMap((frame: DataFrame) => extractSeries(frame, train.frames ?? []));
  if (trained.length === 0) {
    return { forecasts, error: overlayError ?? REASON_TRAIN_EMPTY, usedSaved };
  }

  for (const points of need) {
    const fit = trainingForFit(points, trained);
    if (!fit) {
      continue;
    }
    if (fit.times.length > MAX_TRAIN_POINTS || fit.values.length > MAX_TRAIN_POINTS) {
      overlayError = overlayError ?? REASON_TRAIN_TOO_LONG;
      continue;
    }
    try {
      throwIfAborted(args.signal);
      const key = await args.cacheKeyFor(points.name);
      const resp = await args.post({
        ...args.fitBody,
        cacheKey: key,
        times: fit.times,
        values: fit.values,
        from: args.fromMs,
        to: args.toMs,
        level: args.level,
      });
      const drawn = pushForecast(forecasts, points.name, resp);
      if (!drawn.ok) {
        overlayError = overlayError ?? drawn.reason;
      }
    } catch (e) {
      if (isAbortError(e)) {
        return { forecasts, error: overlayError, usedSaved };
      }
      overlayError = overlayError ?? reasonFromUnknown(e);
      if (isLoadLimitStatus(httpStatusFromUnknown(e))) {
        return { forecasts, error: overlayError, usedSaved };
      }
    }
  }
  return { forecasts, error: overlayError, usedSaved };
}

function isLoadLimitStatus(status: number | undefined): boolean {
  return status === 413 || status === 429 || (status != null && status >= 500);
}

function throwIfAborted(signal?: AbortSignal): void {
  if (!signal?.aborted) {
    return;
  }
  if (signal.reason instanceof Error) {
    throw signal.reason;
  }
  const err = new Error('Aborted');
  err.name = 'AbortError';
  throw err;
}

function pushForecast(
  forecasts: OverlayForecast[],
  name: string,
  resp: ForecastResponse
): { ok: true } | { ok: false; reason: string } {
  const values = (resp.values ?? []).map((v) => (v == null ? NaN : v));
  if (!resp.times?.length) {
    return { ok: false, reason: REASON_EMPTY_WINDOW };
  }
  if (!hasDrawableValues(values)) {
    return { ok: false, reason: REASON_ALL_NAN };
  }
  forecasts.push({
    name: `${name} (forecast)`,
    times: resp.times,
    values,
    lower: resp.lower,
    upper: resp.upper,
  });
  return { ok: true };
}
