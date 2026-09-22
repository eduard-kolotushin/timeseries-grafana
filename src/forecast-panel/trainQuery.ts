import {
  DataFrame,
  DataQuery,
  DataQueryRequest,
  DataQueryResponse,
  DataSourceRef,
  dateTime,
  TimeRange,
} from '@grafana/data';
import { getDataSourceSrv } from '@grafana/runtime';
import { from } from 'rxjs';
import { abortableLastValue, abortError } from './abortable';
import { datasourceUid } from './alertFromPanel';
import { trainMaxDataPoints, trainStepInterval } from './lookback';
import { metricTargets } from './mixed';
import { REASON_TRAIN_EMPTY } from './reasons';
import { rewriteTrainTargets, summarizeTrainTargets, TrainRewriteWindow } from './trainRewrite';

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
  /**
   * Which panel trained this row and on what query. Identification only, for the
   * Retrain schedules table: none of it enters the cacheKey, so adding it cannot
   * orphan a stored snapshot.
   */
  panelId?: number;
  panelTitle?: string;
  dashboardUid?: string;
  querySummary?: string;
};

/** Where the training panel lives, and what it queries. Absent fields serialize away. */
export type TrainProvenance = {
  panelId?: number;
  panelTitle?: string;
  dashboardUid?: string;
  querySummary?: string;
};

export type TrainQueryResult = {
  frames: DataFrame[] | null;
  reason?: string;
  /** Absent when no group returned frames. Only the first producing group is kept. */
  source?: TrainQuerySource;
};

/**
 * The training window plus what the request itself cannot carry: the panel's own
 * datasource (for a target whose own ref is missing) and the schedule-row identity.
 */
export type TrainQueryWindow = TrainRewriteWindow & {
  provenance?: TrainProvenance;
  panelDatasource?: DataSourceRef;
};

/**
 * Keeps only the provenance a panel actually resolved: a partially filled object
 * must not put empty identity into the stored spec or the request body.
 */
export function cleanProvenance(provenance: TrainProvenance | undefined): TrainProvenance {
  const out: TrainProvenance = {};
  if (provenance?.panelId) {
    out.panelId = provenance.panelId;
  }
  if (provenance?.panelTitle) {
    out.panelTitle = provenance.panelTitle;
  }
  if (provenance?.dashboardUid) {
    out.dashboardUid = provenance.dashboardUid;
  }
  if (provenance?.querySummary) {
    out.querySummary = provenance.querySummary;
  }
  return out;
}

/**
 * Run the panel's metric targets once over the training window. When `signal`
 * aborts, the in-flight datasource request is cancelled (not just ignored).
 */
export async function queryTrainingFrames(
  request: DataQueryRequest | undefined,
  window: TrainQueryWindow,
  signal?: AbortSignal
): Promise<TrainQueryResult> {
  const { fromMs, toMs, intervalMs, provenance, panelDatasource } = window;
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

  const groups = groupByDatasource(targets, panelDatasource);

  const frames: DataFrame[] = [];
  let source: TrainQuerySource | undefined;
  let skipReason: string | undefined;
  for (const group of groups.values()) {
    if (signal?.aborted) {
      throw abortError();
    }
    if (!group.key || group.ref == null) {
      // Neither the target nor the panel names a datasource. Resolving `''` would run
      // the training query against the org default, which is not where this panel's
      // series comes from, so the group is dropped instead of fitted from somewhere else.
      skipReason = skipReason ?? REASON_TRAIN_EMPTY;
      continue;
    }
    const ds = await getDataSourceSrv().get(group.ref, scopedVars);
    const rewritten = rewriteTrainTargets(ds.type || refType(group.ref), group.targets, rewriteWindow);
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
        datasourceUid: group.key,
        queries: rewritten.targets,
        from: fromMs,
        to: toMs,
        relative: lookbackMs > 0,
        lookbackMs,
        ...cleanProvenance(provenance),
        // Summarised from the panel's own targets, not the rewritten ones: the rewrite
        // substitutes the train window into SQL text, and a label reads better (and stays
        // stable between a probe and a fit) with the panel's macros in it.
        querySummary: summarizeTrainTargets(ds.type || refType(group.ref), group.targets) || undefined,
      };
      frames.push(...resp.data);
    }
  }
  if (frames.length > 0) {
    return { frames, source };
  }
  return { frames: null, reason: skipReason };
}

/** One datasource's share of the panel's metric targets. */
type DatasourceGroup<T> = {
  key: string;
  ref: DataSourceRef | string | null | undefined;
  targets: T[];
};

/**
 * Targets grouped by the datasource that answers for them, in first-seen order. A target
 * with no ref of its own belongs to the panel's own datasource; it must never fall back to
 * the org default, which is what `getDataSourceSrv().get('')` would resolve to. An empty
 * key is what marks a group as untrainable.
 */
function groupByDatasource<T extends { datasource?: DataQuery['datasource'] }>(
  targets: T[],
  panelDatasource?: DataSourceRef
): Map<string, DatasourceGroup<T>> {
  const groups = new Map<string, DatasourceGroup<T>>();
  for (const target of targets) {
    // The uid the target's own ref names (or its legacy string ref), else the panel's.
    // `refKey` keeps a type-only ref working instead of collapsing it onto the panel.
    const key = datasourceUid(target.datasource, panelDatasource) || refKey(target.datasource);
    const group = groups.get(key);
    if (group) {
      group.targets.push(target);
    } else {
      groups.set(key, { key, ref: target.datasource ?? panelDatasource, targets: [target] });
    }
  }
  return groups;
}

/**
 * The reason a group of the panel's metric targets can never train, or undefined. It mirrors
 * the per-group rejection `queryTrainingFrames` applies, without resolving a datasource or
 * running a query, so a panel that has nothing to draw can still say why nothing will ever be
 * fitted. The type comes from the target ref alone where the fit path also asks the registry:
 * this pre-check stays silent in a case the fit path can still name, and never names one the
 * fit path would not.
 */
export function trainRejectReason(
  request: DataQueryRequest | undefined,
  window: TrainQueryWindow
): string | undefined {
  let reason: string | undefined;
  for (const group of groupByDatasource(metricTargets(request?.targets ?? []), window.panelDatasource).values()) {
    if (!group.key || group.ref == null) {
      // No datasource to run against, so the group can never return frames: the fit path
      // reports the empty training result rather than querying the org default.
      reason = reason ?? REASON_TRAIN_EMPTY;
      continue;
    }
    const rewritten = rewriteTrainTargets(refType(group.ref), group.targets, window);
    if (rewritten.targets.length > 0) {
      // One group the rewrite keeps is enough: `queryTrainingFrames` trains it and reports
      // no reason, so naming one here would refuse a fit the fit path performs.
      return undefined;
    }
    reason = reason ?? rewritten.reason;
  }
  return reason;
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

export function refType(ref: DataSourceRef | string | null | undefined): string {
  if (!ref || typeof ref === 'string') {
    return '';
  }
  return ref.type ?? '';
}
