import { DataFrame } from '@grafana/data';
import { extractSeries, SeriesPoints, trainingForFit } from './extract';
import {
  REASON_ALL_NAN,
  REASON_EMPTY_WINDOW,
  REASON_TRAIN_EMPTY,
  hasDrawableValues,
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
        overlayError = overlayError ?? reasonFromUnknown(e);
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
    train = await args.queryTrain();
  } catch (e) {
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
    try {
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
      overlayError = overlayError ?? reasonFromUnknown(e);
    }
  }
  return { forecasts, error: overlayError, usedSaved };
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
