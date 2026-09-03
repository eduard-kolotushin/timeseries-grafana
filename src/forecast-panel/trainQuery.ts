import {
  DataFrame,
  DataQueryRequest,
  DataQueryResponse,
  DataSourceRef,
  dateTime,
  TimeRange,
} from '@grafana/data';
import { getDataSourceSrv } from '@grafana/runtime';
import { from, lastValueFrom } from 'rxjs';
import { trainMaxDataPoints, trainStepInterval } from './lookback';
import { metricTargets } from './mixed';
import { rewriteTrainTargets, TrainRewriteWindow } from './trainRewrite';

export type TrainQueryResult = {
  frames: DataFrame[] | null;
  reason?: string;
};

export async function queryTrainingFrames(
  request: DataQueryRequest | undefined,
  window: TrainRewriteWindow
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
  let skipReason: string | undefined;
  for (const group of groups.values()) {
    const ds = await getDataSourceSrv().get(group[0].datasource, scopedVars);
    const rewritten = rewriteTrainTargets(ds.type || refType(group[0].datasource), group, rewriteWindow);
    if (rewritten.reason && rewritten.targets.length === 0) {
      skipReason = skipReason ?? rewritten.reason;
      continue;
    }
    if (rewritten.targets.length === 0) {
      continue;
    }
    const resp = (await lastValueFrom(
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
      )
    )) as DataQueryResponse;
    if (resp?.data?.length) {
      frames.push(...resp.data);
    }
  }
  if (frames.length > 0) {
    return { frames };
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
