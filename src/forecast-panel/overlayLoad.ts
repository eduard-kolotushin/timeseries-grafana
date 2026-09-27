import { DataFrame } from '@grafana/data';
import { extractSeries, SeriesPoints, trainingForFit } from './extract';
import { MAX_PANEL_KEYS, MAX_TRAIN_POINTS } from './lookback';
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
import { TrainQueryResult, TrainProvenance, cleanProvenance } from './trainQuery';
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
  /**
   * Which panel is asking. It rides on every request, the probe included, so a
   * schedule row written before the plugin stored provenance is identified without
   * waiting for a refit.
   */
  provenance?: TrainProvenance;
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
  const provenance = cleanProvenance(args.provenance);
  const identify = Object.keys(provenance).length > 0 ? { provenance } : {};

  if (!args.retrain) {
    for (const points of args.visible) {
      try {
        throwIfAborted(args.signal);
        const key = await args.cacheKeyFor(points.name);
        const resp = await args.post({
          ...args.fitBody,
          ...identify,
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

  const trainSource = train.source;
  // Every visible series' key, named on each fit: the backend retires the panel's rows
  // that are absent from that set (Supersede), and the overlay POSTs one fit per series,
  // so a fit naming only its own key would retire the panel's other series on every load.
  // Computed once, before the first POST, because every fit carries the same set.
  const panelKeys: string[] = [];
  for (const points of args.visible) {
    try {
      panelKeys.push(await args.cacheKeyFor(points.name));
    } catch {
      // A series whose key cannot be computed is simply not part of the set; its own fit
      // reports the failure below.
    }
  }
  // A panel showing more series than the backend accepts in one request cannot name its
  // whole set, and a partial one would retire the series it left out on every load. So
  // such a panel sends no set at all: Supersede then keys off the fit's own key, which is
  // the behaviour every panel had before the set existed (its rows keep flipping).
  const panelKeyField = panelKeys.length <= MAX_PANEL_KEYS ? { panelKeys } : {};
  for (const points of need) {
    const fit = trainingForFit(points, trained);
    if (!fit) {
      // The training frame has no series for this one; say so rather than draw it with no
      // reason. A fit that could only be trained from one of several datasources says why.
      overlayError = overlayError ?? train.droppedReason ?? REASON_TRAIN_EMPTY;
      continue;
    }
    if (fit.times.length > MAX_TRAIN_POINTS || fit.values.length > MAX_TRAIN_POINTS) {
      overlayError = overlayError ?? REASON_TRAIN_TOO_LONG;
      continue;
    }
    try {
      throwIfAborted(args.signal);
      const key = await args.cacheKeyFor(points.name);
      const body: OverlayPostBody = {
        ...args.fitBody,
        ...identify,
        cacheKey: key,
        ...panelKeyField,
        times: fit.times,
        values: fit.values,
        from: args.fromMs,
        to: args.toMs,
        level: args.level,
      };
      if (trainSource) {
        // `seriesName` is the matched *training* series: the backend later re-extracts it
        // out of the replayed frame, and the visible display name need not match it. A
        // relative window rides along untouched: the backend re-resolves it at claim time.
        body.trainSource = { ...trainSource, seriesName: fit.name };
      }
      const resp = await args.post(body);
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
