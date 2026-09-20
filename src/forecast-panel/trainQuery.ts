import {
  DataFrame,
  DataQueryRequest,
  DataQueryResponse,
  DataSourceRef,
  dateTime,
  TimeRange,
} from '@grafana/data';
import { getDataSourceSrv } from '@grafana/runtime';
import { from } from 'rxjs';
import { abortableLastValue, abortError } from './abortable';
import { trainMaxDataPoints, trainStepInterval } from './lookback';
import { metricTargets } from './mixed';
import { rewriteTrainTargets, TrainRewriteWindow } from './trainRewrite';

/**
 * The datasource query objects that produced the training frames, verbatim, plus the
 * train window. The overlay POSTs this so the plugin backend can replay it through
 * Grafana's own `/api/ds/query` on a schedule, with no browser open.
 */
export type TrainQuerySource = {
  datasourceUid: string;
  queries: unknown[];
  from: number;
  to: number;
  /**
   * True when `from`/`to` were derived from a lookback: the backend re-resolves
   * `[now - lookbackMs, now]` at retrain time instead of replaying them. Absent
   * (undefined) serializes away, which the backend reads as an absolute window.
   */
  relative?: boolean;
  lookbackMs?: number;
};

export type TrainQueryResult = {
  frames: DataFrame[] | null;
  reason?: string;
  /** Absent when no group returned frames. Only the first producing group is kept. */
  source?: TrainQuerySource;
};

/**
 * Run the panel's metric targets once over the training window. When `signal`
 * aborts, the in-flight datasource request is cancelled (not just ignored).
 */
export async function queryTrainingFrames(
  request: DataQueryRequest | undefined,
  window: TrainRewriteWindow,
  signal?: AbortSignal
): Promise<TrainQueryResult> {
  const { fromMs, toMs, intervalMs } = window;
  const targets = metricTargets(request?.targets ?? []);
  if (!request || targets.length === 0 || !Number.isFinite(fromMs) || !Number.isFinite(toMs) || toMs <= fromMs) {
    return { frames: null };
  }

  const visibleFromMs = window.visibleFromMs ?? request.range?.from?.valueOf();
  const visibleToMs = window.visibleToMs ?? request.range?.to?.valueOf();
  const rewriteWindow: TrainRewriteWindow = {
    fromMs,
    toMs,
    visibleFromMs,
    visibleToMs,
    intervalMs,
  };
  const range: TimeRange = {
    from: dateTime(fromMs),
    to: dateTime(toMs),
    raw: { from: dateTime(fromMs), to: dateTime(toMs) },
  };
  const stepMs = intervalMs > 0 ? intervalMs : 60_000;
  const maxDataPoints = trainMaxDataPoints(toMs - fromMs, stepMs);
  const interval = trainStepInterval(stepMs);
  // The backend re-resolves a lookback window at retrain time; a window that is
  // not a lookback has no width to re-resolve.
  const lookbackMs = window.relative === true ? Math.max(window.lookbackMs ?? 0, 0) : 0;
  const scopedVars = {
    ...request.scopedVars,
    __from: { text: String(fromMs), value: String(fromMs) },
    __to: { text: String(toMs), value: String(toMs) },
  };

  const groups = new Map<string, typeof targets>();
  for (const target of targets) {
    const key = refKey(target.datasource);
    const group = groups.get(key);
    if (group) {
      group.push(target);
    } else {
      groups.set(key, [target]);
    }
  }

  const frames: DataFrame[] = [];
  let source: TrainQuerySource | undefined;
  let skipReason: string | undefined;
  for (const group of groups.values()) {
    if (signal?.aborted) {
      throw abortError();
    }
    const ds = await getDataSourceSrv().get(group[0].datasource, scopedVars);
    const rewritten = rewriteTrainTargets(ds.type || refType(group[0].datasource), group, rewriteWindow);
    if (rewritten.reason && rewritten.targets.length === 0) {
      skipReason = skipReason ?? rewritten.reason;
      continue;
    }
    if (rewritten.targets.length === 0) {
      continue;
    }
    const resp = (await abortableLastValue(
      from(
        ds.query({
          ...request,
          targets: rewritten.targets,
          range,
          rangeRaw: range.raw,
          startTime: Date.now(),
          interval,
          intervalMs: stepMs,
          maxDataPoints,
          scopedVars,
          requestId: `${request.requestId ?? 'forecast'}-train`,
        })
      ),
      signal
    )) as DataQueryResponse;
    if (resp?.data?.length) {
      // The backend replays these exact objects and re-extracts the training series by
      // name, so they stay untouched here (`rewritten.targets` is already a clone).
      source = source ?? {
        datasourceUid: refKey(group[0].datasource),
        queries: rewritten.targets,
        from: fromMs,
        to: toMs,
        relative: lookbackMs > 0,
        lookbackMs,
      };
      frames.push(...resp.data);
    }
  }
  if (frames.length > 0) {
    return { frames, source };
  }
  return { frames: null, reason: skipReason };
}

function refKey(ref: DataSourceRef | string | null | undefined): string {
  if (!ref) {
    return '';
  }
  if (typeof ref === 'string') {
    return ref;
  }
  return ref.uid ?? ref.type ?? '';
}

function refType(ref: DataSourceRef | string | null | undefined): string {
  if (!ref || typeof ref === 'string') {
    return '';
  }
  return ref.type ?? '';
}
