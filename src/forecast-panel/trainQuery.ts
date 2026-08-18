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
import { applyLookbackRange, trainMaxDataPoints } from './lookback';

export async function queryTrainingFrames(
  request: DataQueryRequest | undefined,
  lookbackMs: number,
  toMs: number
): Promise<DataFrame[] | null> {
  const targets = request?.targets?.filter((t) => !t.hide) ?? [];
  if (!request || targets.length === 0 || lookbackMs <= 0) {
    return null;
  }

  const fromMs = toMs - lookbackMs;
  const visibleFromMs = request.range?.from?.valueOf();
  const visibleToMs = request.range?.to?.valueOf();
  const range: TimeRange = {
    from: dateTime(fromMs),
    to: dateTime(toMs),
    raw: { from: dateTime(fromMs), to: dateTime(toMs) },
  };
  const intervalMs = request.intervalMs > 0 ? request.intervalMs : 60_000;
  const maxDataPoints = trainMaxDataPoints(lookbackMs, intervalMs);
  const scopedVars = {
    ...request.scopedVars,
    __from: { text: String(fromMs), value: String(fromMs) },
    __to: { text: String(toMs), value: String(toMs) },
  };

  const rewritten = applyLookbackRange(targets, fromMs, toMs, visibleFromMs, visibleToMs);
  const groups = new Map<string, typeof rewritten>();
  for (const target of rewritten) {
    const key = refKey(target.datasource);
    const group = groups.get(key);
    if (group) {
      group.push(target);
    } else {
      groups.set(key, [target]);
    }
  }

  const frames: DataFrame[] = [];
  for (const group of groups.values()) {
    const ds = await getDataSourceSrv().get(group[0].datasource, scopedVars);
    const resp = (await lastValueFrom(
      from(
        ds.query({
          ...request,
          targets: group,
          range,
          rangeRaw: range.raw,
          startTime: Date.now(),
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
  return frames.length > 0 ? frames : null;
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
