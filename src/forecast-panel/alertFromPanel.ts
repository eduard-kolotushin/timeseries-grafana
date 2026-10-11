import { DataFrame, rangeUtil } from '@grafana/data';
import { getDataSourceSrv, locationService } from '@grafana/runtime';
import { cacheKeyInputFromQuery, queryFromOptions } from '../forecast-datasource/queryModel';
import { FORECAST_DATASOURCE_TYPE } from '../forecast-datasource/types';
import { cacheKey } from './cacheKey';
import { extractSeries } from './extract';
import { isForecastTarget, splitPanelFrames } from './mixed';
import { ForecastOptions } from './types';

export const EXPRESSION_DATASOURCE_UID = '__expr__';
export const EXPRESSION_DATASOURCE_TYPE = '__expr__';

export const REASON_DASHBOARD_NOT_SAVED = 'Dashboard must be saved before alerts can be added.';
export const REASON_NO_ALERTING_QUERY =
  'Cannot create alerts from this panel because no query to an alerting capable datasource is found.';
export const REASON_ALERT_SERIES_REQUIRED = 'Choose the metric series this alert rule is for.';
export const REASON_ALERT_SERIES_MISSING =
  'No matching metric series in this panel. Load it once, then pick the series this alert rule is for.';
export const REASON_FORECAST_DATASOURCE =
  'Cannot create an alert rule: add a Forecast datasource query to this panel, or keep exactly one Forecast datasource instance.';

export type RelativeTimeRange = { from: number; to: number };

export type DatasourceRef = { uid?: string; type?: string } | string | null | undefined;

export type PanelTarget = {
  refId?: string;
  hide?: boolean;
  datasource?: DatasourceRef;
  queryType?: string;
  [key: string]: unknown;
};

export type LivePanel = {
  id?: number;
  title?: string;
  targets?: PanelTarget[];
  datasource?: DatasourceRef;
  maxDataPoints?: number;
  panels?: LivePanel[];
};

export type DashboardSaveModel = {
  uid?: string;
  title?: string;
  time?: { from: string; to: string };
  panels?: LivePanel[];
};

export type AlertQuery = {
  refId: string;
  queryType: string;
  relativeTimeRange: RelativeTimeRange;
  datasourceUid: string;
  model: Record<string, unknown>;
};

export type RuleFormDefaults = {
  type: 'grafana-alerting';
  name: string;
  queries: AlertQuery[];
  condition: string;
  annotations: Array<{ key: string; value: string }>;
};

export type AlertFromPanelResult = { ok: true; defaults: RuleFormDefaults } | { ok: false; reason: string };

/** A series the panel has loaded, as the fit that wrote the snapshot named it. */
export type PanelSeries = { name: string; refId: string; labels?: Record<string, string> };

export const DEFAULT_RELATIVE_TIME_RANGE: RelativeTimeRange = { from: 600, to: 0 };

/** Percent deviation from the baseline the rule fires above unless the panel says otherwise. */
export const DEFAULT_ALERT_TOLERANCE = 20;

export function datasourceUid(ref: DatasourceRef, fallback?: DatasourceRef): string {
  const picked = ref ?? fallback;
  if (!picked) {
    return '';
  }
  if (typeof picked === 'string') {
    return picked;
  }
  return picked.uid ?? '';
}

/**
 * The datasources a rule built from this panel may draw on: the ones whose plugin reports
 * `meta.alerting`. A panel `__expr__` row is deliberately not one of them — the rule builds
 * its own reduce/math/threshold chain from the panel's fitted series, so reusing a panel's
 * expression rows would put unrelated nodes in front of the condition.
 */
export function alertingUidsFor(targets: PanelTarget[], panelDs: DatasourceRef): Set<string> {
  const uids = new Set<string>();
  const srv = getDataSourceSrv();
  for (const target of targets) {
    const uid = datasourceUid(target.datasource, panelDs);
    if (!uid || uids.has(uid)) {
      continue;
    }
    if (srv.getInstanceSettings(uid)?.meta.alerting) {
      uids.add(uid);
    }
  }
  return uids;
}

export function dashboardUidFromPath(pathname: string): string | undefined {
  const m = pathname.match(/\/d\/([^/?]+)/);
  const uid = m?.[1];
  return uid && uid !== 'new' ? uid : undefined;
}

export function asSaveModel(raw: unknown): DashboardSaveModel | undefined {
  if (!raw || typeof raw !== 'object') {
    return undefined;
  }
  const o = raw as Record<string, unknown>;
  if (Array.isArray(o.panels) || typeof o.uid === 'string' || o.time) {
    return o as DashboardSaveModel;
  }
  if (o.dashboard && typeof o.dashboard === 'object') {
    return asSaveModel(o.dashboard);
  }
  return undefined;
}

export function findPanel(panels: LivePanel[] | undefined, panelId: number): LivePanel | undefined {
  for (const panel of panels ?? []) {
    if (panel.id === panelId) {
      return panel;
    }
    const nested = findPanel(panel.panels, panelId);
    if (nested) {
      return nested;
    }
  }
  return undefined;
}

function nextRefId(queries: Array<{ refId: string }>): string {
  let code = 'A'.charCodeAt(0);
  let refId = 'A';
  while (queries.some((q) => q.refId === refId)) {
    code += 1;
    refId = String.fromCharCode(code);
  }
  return refId;
}

/** The model datasource of an expression node, as Grafana's own expression rows carry it. */
const EXPRESSION_DATASOURCE = { uid: EXPRESSION_DATASOURCE_UID, type: EXPRESSION_DATASOURCE_TYPE };

/**
 * A Grafana duration (`10m`, `1h`, `600s`, or a bare second count) as the rule's window:
 * both the metric query and the baseline query read `now - <lookback> … now`, so their
 * last values come from the same instant. An empty or unparseable value keeps the default.
 */
export function windowFrom(alertLookback: string | undefined): RelativeTimeRange {
  const raw = (alertLookback ?? '').trim();
  if (!raw) {
    return DEFAULT_RELATIVE_TIME_RANGE;
  }
  try {
    const seconds = Math.round(rangeUtil.intervalToMs(raw) / 1000);
    if (Number.isFinite(seconds) && seconds > 0) {
      return { from: seconds, to: 0 };
    }
  } catch {
    // An unparseable lookback falls back to the default rather than producing a broken rule.
  }
  return DEFAULT_RELATIVE_TIME_RANGE;
}

/**
 * The panel's visible range in ms. The cacheKey fingerprint collapses timestamps a macro
 * has already interpolated into absolute times, so a key computed without it would differ
 * from the one the overlay fit wrote.
 */
export function absoluteRangeMs(time?: { from: string; to: string }): {
  visibleFromMs?: number;
  visibleToMs?: number;
} {
  if (!time?.from || !time?.to) {
    return {};
  }
  try {
    const range = rangeUtil.convertRawToRange(time);
    return { visibleFromMs: range.from.valueOf(), visibleToMs: range.to.valueOf() };
  } catch {
    return {};
  }
}

/**
 * The panel's metric series, as the fit names them: history frames only (a Forecast row's
 * own frames are not metrics), one entry per graphable field, first mention wins — the same
 * `getFieldDisplayName` identity and the same `pickTrainingPoints`-style single candidate
 * the overlay fits by.
 */
export function panelSeries(frames: DataFrame[] | undefined, targets: PanelTarget[] | undefined): PanelSeries[] {
  const all = frames ?? [];
  const history = splitPanelFrames(all, targets ?? []).history;
  const out: PanelSeries[] = [];
  const seen = new Set<string>();
  for (const frame of history) {
    for (const points of extractSeries(frame, history)) {
      if (seen.has(points.name)) {
        continue;
      }
      seen.add(points.name);
      out.push({
        name: points.name,
        refId: typeof frame.refId === 'string' ? frame.refId : '',
        labels: points.labels,
      });
    }
  }
  return out;
}

/**
 * The rows a rule may draw on: visible, not a Forecast row, not a panel expression row, and
 * on a datasource that reports `meta.alerting`.
 */
export function metricAlertTargets(
  targets: PanelTarget[],
  panelDs: DatasourceRef,
  alertingUids: Set<string>
): PanelTarget[] {
  return targets.filter((target) => {
    if (target.hide || isForecastTarget(target)) {
      return false;
    }
    const uid = datasourceUid(target.datasource, panelDs);
    return Boolean(uid) && uid !== EXPRESSION_DATASOURCE_UID && alertingUids.has(uid);
  });
}

/**
 * The Forecast datasource instance a rule should read through: the uid of the panel's own
 * Forecast row when it has one, otherwise the deployment's single Forecast datasource
 * instance (both the Compose sandbox and the Helm chart provision exactly one).
 */
export function forecastDatasourceUid(
  targets: PanelTarget[],
  srv: { getList: () => Array<{ uid: string; type?: string }> } = getDataSourceSrv()
): string | undefined {
  for (const target of targets) {
    if (!isForecastTarget(target)) {
      continue;
    }
    const uid = datasourceUid(target.datasource);
    if (uid) {
      return uid;
    }
  }
  const instances = srv.getList().filter((item) => item.type === FORECAST_DATASOURCE_TYPE);
  return instances.length === 1 ? instances[0].uid : undefined;
}

function liveSaveModel(): DashboardSaveModel | undefined {
  const runtime = (window as Window & { grafanaRuntime?: { getDashboardSaveModel?: () => unknown } }).grafanaRuntime;
  try {
    return asSaveModel(runtime?.getDashboardSaveModel?.());
  } catch {
    return undefined;
  }
}

/** The panel id Grafana put in the URL while the panel editor is open. */
export function panelIdFromLocation(): number {
  return Number(locationService.getSearchObject().editPanel);
}

/**
 * The panel as it is right now: the unsaved scene's targets when the editor has them, the
 * saved dashboard's model otherwise, plus the dashboard's uid and time range. Both the
 * alert button and the Alert series option read the panel through this one path.
 */
export function livePanel(panelId: number): {
  dashboardUid?: string;
  panel?: LivePanel;
  time?: { from: string; to: string };
} {
  const dash = (window as Window & { __grafanaSceneContext?: unknown }).__grafanaSceneContext as
    | {
        state?: {
          uid?: string;
          $timeRange?: unknown;
          editPanel?: { state?: { panelRef?: { resolve?: () => unknown } } };
        };
      }
    | undefined;
  const viz = dash?.state?.editPanel?.state?.panelRef?.resolve?.();
  const runner = findQueryRunnerState(viz);
  const title =
    viz && typeof viz === 'object' && 'state' in viz
      ? String((viz as { state?: { title?: string } }).state?.title ?? '')
      : '';
  const model = liveSaveModel();
  const scenePanel: LivePanel = {
    id: panelId,
    title,
    targets: runner?.queries,
    datasource: runner?.datasource,
    maxDataPoints: runner?.maxDataPoints,
  };
  return {
    dashboardUid: model?.uid || dash?.state?.uid,
    time: dashboardTimeFromScene(dash) ?? model?.time,
    panel: pickLivePanel(findPanel(model?.panels, panelId), scenePanel, panelId),
  };
}

function pickSeries(series: PanelSeries[], alertSeries?: string): { series: PanelSeries } | { reason: string } {
  if (series.length === 0) {
    return { reason: REASON_ALERT_SERIES_MISSING };
  }
  const wanted = alertSeries?.trim();
  if (!wanted) {
    return series.length > 1 ? { reason: REASON_ALERT_SERIES_REQUIRED } : { series: series[0] };
  }
  const found = series.find((s) => s.name === wanted);
  return found ? { series: found } : { reason: REASON_ALERT_SERIES_MISSING };
}

function reduceExpression(refId: string, sourceRefId: string, window: RelativeTimeRange): AlertQuery {
  return {
    refId,
    datasourceUid: EXPRESSION_DATASOURCE_UID,
    queryType: 'expression',
    relativeTimeRange: window,
    model: {
      refId,
      type: 'reduce',
      datasource: EXPRESSION_DATASOURCE,
      reducer: 'last',
      expression: sourceRefId,
      conditions: [
        {
          type: 'query',
          evaluator: { params: [], type: 'gt' },
          operator: { type: 'and' },
          query: { params: [] },
          reducer: { params: [], type: 'last' },
        },
      ],
    },
  };
}

function mathExpression(refId: string, formula: string, window: RelativeTimeRange): AlertQuery {
  return {
    refId,
    datasourceUid: EXPRESSION_DATASOURCE_UID,
    queryType: 'expression',
    relativeTimeRange: window,
    model: {
      refId,
      type: 'math',
      datasource: EXPRESSION_DATASOURCE,
      expression: formula,
    },
  };
}

/**
 * The condition: a threshold on the Math node. Grafana fires a condition node on a non-zero
 * value, so an absolute percent deviation above the tolerance is what alerts — in both
 * directions, and without the operator seeing a bare 0/1 comparison. The evaluator is the
 * model-level one the expression engine reads; the nested `conditions` block is the legacy
 * shape the rule form still shows, kept in step with it.
 */
function thresholdExpression(
  refId: string,
  sourceRefId: string,
  tolerance: number,
  window: RelativeTimeRange
): AlertQuery {
  return {
    refId,
    datasourceUid: EXPRESSION_DATASOURCE_UID,
    queryType: 'expression',
    relativeTimeRange: window,
    model: {
      refId,
      type: 'threshold',
      datasource: EXPRESSION_DATASOURCE,
      expression: sourceRefId,
      evaluator: { type: 'gt', params: [tolerance] },
      conditions: [
        {
          type: 'query',
          evaluator: { params: [tolerance], type: 'gt' },
          operator: { type: 'and' },
          query: { params: [refId] },
          reducer: { params: [], type: 'last' },
        },
      ],
    },
  };
}

/**
 * A complete Grafana alert rule from the state of a trained overlay panel: the panel's
 * metric rows, one synthesized Forecast row that restores the snapshot the overlay fit
 * wrote, Reduce(last) over each, a Math percent deviation, and a Threshold on it as the
 * alert condition. `series` is what the panel has loaded — the caller reads it from the
 * frames so a stale `alertSeries` names a series that is really there.
 */
export async function ruleDefaultsFromPanel(input: {
  dashboardUid?: string;
  panel?: LivePanel;
  panelId?: number;
  options?: ForecastOptions;
  series: PanelSeries[];
  metricTargets: PanelTarget[];
  visibleFromMs?: number;
  visibleToMs?: number;
  alertingUids: Set<string>;
  forecastDatasourceUid?: string;
}): Promise<AlertFromPanelResult> {
  const dashboardUid = input.dashboardUid?.trim();
  if (!dashboardUid) {
    return { ok: false, reason: REASON_DASHBOARD_NOT_SAVED };
  }
  const panelId = input.panelId ?? input.panel?.id;
  if (panelId == null || !Number.isFinite(Number(panelId))) {
    return { ok: false, reason: REASON_DASHBOARD_NOT_SAVED };
  }

  const panelDs = input.panel?.datasource;
  const rows = metricAlertTargets(input.metricTargets, panelDs, input.alertingUids);
  if (rows.length === 0) {
    return { ok: false, reason: REASON_NO_ALERTING_QUERY };
  }

  const picked = pickSeries(input.series, input.options?.alertSeries);
  if ('reason' in picked) {
    return { ok: false, reason: picked.reason };
  }
  const chosen = picked.series;

  const forecastUid = input.forecastDatasourceUid?.trim();
  if (!forecastUid) {
    return { ok: false, reason: REASON_FORECAST_DATASOURCE };
  }

  const window = windowFrom(input.options?.alertLookback);
  const queries: AlertQuery[] = [];
  // One series is compared with one baseline, so the rule carries the metric rows that
  // produced the chosen series — every visible metric target still rides along in the
  // forecast query's sourceTargets, which is what the cacheKey is computed from.
  const seriesRows = rows.filter((target) => !chosen.refId || target.refId === chosen.refId);
  for (const target of seriesRows.length > 0 ? seriesRows : rows) {
    const refId = typeof target.refId === 'string' && target.refId ? target.refId : nextRefId(queries);
    const { hide: _hide, ...model } = target;
    queries.push({
      refId,
      queryType: target.queryType ?? '',
      relativeTimeRange: window,
      datasourceUid: datasourceUid(target.datasource, panelDs),
      model: { ...model, refId, maxDataPoints: input.panel?.maxDataPoints },
    });
  }
  const metricRefId = queries[0].refId;

  // The synthesized row carries the overlay's fit options and every visible metric target,
  // so the key below is the one the overlay wrote. It is derived from this very query, which
  // is what makes the two impossible to drift apart.
  const forecastRefId = nextRefId(queries);
  const forecastModel = queryFromOptions({
    refId: forecastRefId,
    options: input.options,
    sourceTargets: input.metricTargets,
    seriesName: chosen.name,
    seriesLabels: chosen.labels,
    datasource: { uid: forecastUid, type: FORECAST_DATASOURCE_TYPE },
  });
  const key = await cacheKey({
    ...cacheKeyInputFromQuery(forecastModel),
    visibleFromMs: input.visibleFromMs,
    visibleToMs: input.visibleToMs,
  });
  // A rule's model is an opaque object to Grafana; the synthesized Forecast query is the
  // whole model plus the key the overlay wrote and the panel's resolution, exactly as the
  // copied metric rows carry it.
  const forecastQueryModel: Record<string, unknown> = {
    ...forecastModel,
    cacheKey: key,
    maxDataPoints: input.panel?.maxDataPoints,
  };
  queries.push({
    refId: forecastRefId,
    queryType: '',
    relativeTimeRange: window,
    datasourceUid: forecastUid,
    model: forecastQueryModel,
  });

  const metricReduce = nextRefId(queries);
  queries.push(reduceExpression(metricReduce, metricRefId, window));
  const baselineReduce = nextRefId(queries);
  queries.push(reduceExpression(baselineReduce, forecastRefId, window));
  const deviation = nextRefId(queries);
  queries.push(
    mathExpression(deviation, `abs(($${metricReduce} - $${baselineReduce}) / $${baselineReduce}) * 100`, window)
  );
  const conditionRefId = nextRefId(queries);
  queries.push(
    thresholdExpression(conditionRefId, deviation, input.options?.alertTolerance ?? DEFAULT_ALERT_TOLERANCE, window)
  );

  return {
    ok: true,
    defaults: {
      type: 'grafana-alerting',
      name: input.panel?.title || 'New alert rule',
      queries,
      condition: conditionRefId,
      annotations: [
        { key: '__dashboardUid__', value: dashboardUid },
        { key: '__panelId__', value: String(panelId) },
      ],
    },
  };
}

export function findQueryRunnerState(
  root: unknown,
  depth = 0
): { queries?: PanelTarget[]; datasource?: DatasourceRef; maxDataPoints?: number } | undefined {
  if (!root || typeof root !== 'object' || depth > 8) {
    return undefined;
  }
  const st = (root as { state?: Record<string, unknown> }).state;
  if (!st) {
    return undefined;
  }
  if (Array.isArray(st.queries)) {
    return {
      queries: st.queries as PanelTarget[],
      datasource: st.datasource as DatasourceRef,
      maxDataPoints: typeof st.maxDataPoints === 'number' ? st.maxDataPoints : undefined,
    };
  }
  for (const v of Object.values(st)) {
    const found = findQueryRunnerState(v, depth + 1);
    if (found) {
      return found;
    }
  }
  return undefined;
}

export function dashboardTimeFromScene(root: unknown): { from: string; to: string } | undefined {
  const st = root && typeof root === 'object' ? (root as { state?: Record<string, unknown> }).state : undefined;
  const tr = st?.$timeRange as { state?: { value?: { raw?: { from?: unknown; to?: unknown } } } } | undefined;
  const raw = tr?.state?.value?.raw;
  if (typeof raw?.from === 'string' && typeof raw?.to === 'string') {
    return { from: raw.from, to: raw.to };
  }
  return undefined;
}

export function pickLivePanel(saved: LivePanel | undefined, scene: LivePanel | undefined, panelId: number): LivePanel {
  if (scene?.targets && scene.targets.length > 0) {
    return { ...scene, id: panelId };
  }
  return { ...(saved ?? scene ?? {}), id: panelId };
}

export function alertingNewPath(defaults: RuleFormDefaults, returnTo: string): string {
  const params = new URLSearchParams({
    defaults: JSON.stringify(defaults),
    returnTo,
  });
  return `/alerting/new?${params.toString()}`;
}
