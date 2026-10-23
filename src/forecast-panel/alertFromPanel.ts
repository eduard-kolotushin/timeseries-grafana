import { DataFrame, rangeUtil } from '@grafana/data';
import { config, getDataSourceSrv, locationService } from '@grafana/runtime';
import { cacheKey } from './cacheKey';
import { FORECAST_DATASOURCE_TYPE } from '../forecast-datasource/types';
import { extractSeries, graphableFrames } from './extract';
import { isForecastTarget, splitPanelFrames } from './mixed';
import { DEFAULT_FORECAST_OPTIONS, ForecastOptions } from './types';

export const EXPRESSION_DATASOURCE_UID = '__expr__';
export const EXPRESSION_DATASOURCE_TYPE = '__expr__';

export const REASON_DASHBOARD_NOT_SAVED = 'Dashboard must be saved before alerts can be added.';
export const REASON_NO_ALERTING_QUERY =
  'Cannot create alerts from this panel because no query to an alerting capable datasource is found.';
export const REASON_ALERT_SERIES_REQUIRED = 'Choose the metric series this alert rule is for.';
export const REASON_ALERT_SERIES_MISSING =
  'No matching metric series in this panel. Load it once, then pick the series this alert rule is for.';
export const REASON_ALERT_SERIES_AMBIGUOUS =
  'This panel’s series carry no labels and no distinct series name, so the alert rule cannot say which one it is for. Give the query a per-series column or label, or split the series into separate panels.';
export const REASON_ALERT_SERIES_NOT_ALERTING =
  'The selected series is not produced by an alerting query; pick a series from a query that can alert.';
export const REASON_ALERT_SQL_OFF =
  'This panel’s series carry no labels, so the rule has to pick one inside a SQL expression, and SQL expressions are off in this Grafana. Enable the `sqlExpressions` feature toggle, or label the series.';
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
export type PanelSeries = {
  name: string;
  refId: string;
  labels?: Record<string, string>;
  /** The source field's raw name — Grafana's `__metric_name__` inside an expression. */
  fieldName: string;
  /** The field's configured display name (never what Grafana's own column carries). */
  displayName?: string;
  /** The datasource's own display name; the `__display_name__` column. */
  displayNameFromDS?: string;
  /** Set on a legacy (non-dataplane) frame, the only path where Grafana synthesizes `__name__`. */
  legacyMeta: boolean;
};

export const DEFAULT_RELATIVE_TIME_RANGE: RelativeTimeRange = { from: 600, to: 0 };

/** Percent deviation from the baseline the rule fires above unless the panel says otherwise. */
export const DEFAULT_ALERT_TOLERANCE = DEFAULT_FORECAST_OPTIONS.alertTolerance ?? 20;

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
export function absoluteRangeMs(
  time?: { from: string; to: string },
  timeZone?: Parameters<typeof rangeUtil.convertRawToRange>[1]
): {
  visibleFromMs?: number;
  visibleToMs?: number;
} {
  if (!time?.from || !time?.to) {
    return {};
  }
  try {
    // The panel resolves its request range in the dashboard timezone; resolve the
    // same picker text the same way so an absolute range cannot change the key.
    const range = rangeUtil.convertRawToRange(time, timeZone);
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
  // Same two steps as the overlay: the filter is judged over the split history,
  // then names are read from the drawable subset.
  const graphable = graphableFrames(history);
  const out: PanelSeries[] = [];
  const seen = new Set<string>();
  for (const frame of graphable) {
    for (const points of extractSeries(frame, graphable)) {
      if (seen.has(points.name)) {
        continue;
      }
      seen.add(points.name);
      out.push({
        name: points.name,
        refId: typeof frame.refId === 'string' ? frame.refId : '',
        labels: points.labels,
        fieldName: points.fieldName || points.name,
        displayName: points.config?.displayName,
        displayNameFromDS: points.config?.displayNameFromDS,
        legacyMeta: !points.meta?.type || isZeroTypeVersion(points.meta.typeVersion),
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

/**
 * `data.FrameTypeVersion.IsZero()`: the wire form is `[major, minor]`, so a frame whose
 * datasource set a type but a zero version (`[0, 0]`) still takes the legacy conversion
 * path — which is where Grafana synthesizes `__name__`. Only a non-zero version means the
 * dataplane path, where nothing is synthesized.
 */
function isZeroTypeVersion(version: unknown): boolean {
  if (version == null) {
    return true;
  }
  const parts = Array.isArray(version) ? version : [version];
  return parts.every((part) => Number(part) === 0);
}

/** The datasources whose legacy frames Grafana names itself (`pkg/expr/converter.go`). */
const SYNTHESIZING_DATASOURCE_TYPES = new Set([
  'graphite',
  'grafana-testdata-datasource',
  'grafana-dynatrace-datasource',
  'influxdb',
]);

/**
 * How the rule tells Grafana which series it is for. Labels are what the Math node pairs
 * by; `name` mirrors the `__name__` Grafana itself synthesizes for a label-free legacy
 * frame; `column` is a `sql` condition selecting the row out of the evaluation's table.
 */
export type SeriesTarget =
  | { kind: 'labels'; labels: Record<string, string> }
  | { kind: 'name'; name: string }
  | { kind: 'column'; column: '__metric_name__' | '__display_name__'; value: string }
  | { kind: 'refuse'; reason: string };

type SeriesSelector = 'displayNameFromDS' | 'displayName' | 'fieldName';

function selectorValue(series: PanelSeries, key: SeriesSelector): string {
  return (series[key] ?? '').trim();
}

/** A selector can name one series of the row when every series has a value and no two share it. */
function distinctSelector(row: PanelSeries[], key: SeriesSelector): boolean {
  const values = row.map((s) => selectorValue(s, key));
  return values.every((v) => v !== '') && new Set(values).size === values.length;
}

/**
 * The datasource type of the row the chosen series came from: a target usually carries its
 * own `{uid, type}`, and a scene query that carries only a uid is resolved through Grafana's
 * datasource registry.
 */
export function targetDatasourceType(
  row: PanelSeries[],
  rows: PanelTarget[],
  chosen: PanelSeries,
  panelDs: DatasourceRef
): string {
  const target = (chosen.refId ? rows.find((t) => t.refId === chosen.refId) : undefined) ?? rows[0];
  const ref = target?.datasource;
  if (ref && typeof ref === 'object' && typeof ref.type === 'string' && ref.type) {
    return ref.type;
  }
  const uid = datasourceUid(ref, panelDs);
  return uid ? getDataSourceSrv().getInstanceSettings(uid)?.type ?? '' : '';
}

/**
 * How this rule can say which series it is for. Anything labelled pairs by label. A
 * label-free row of several series has no label to pair with, so it is named the way the
 * evaluation names it: Grafana's own synthesized `__name__` for a datasource it does that
 * for, and otherwise the column the SQL condition selects (`__display_name__` /
 * `__metric_name__`). A row nothing can name is refused, because Grafana would otherwise
 * broadcast the one baseline across every series and alert on series nobody picked.
 */
export function seriesTarget(series: PanelSeries[], chosen: PanelSeries, datasourceType: string): SeriesTarget {
  const row = chosen.refId ? series.filter((s) => s.refId === chosen.refId) : series;
  if (row.length <= 1) {
    return { kind: 'labels', labels: chosen.labels ?? {} };
  }
  if (chosen.labels && Object.keys(chosen.labels).length > 0) {
    return { kind: 'labels', labels: chosen.labels };
  }
  const named =
    SYNTHESIZING_DATASOURCE_TYPES.has(datasourceType) && row.every((s) => s.legacyMeta);
  if (named) {
    for (const key of ['displayNameFromDS', 'displayName', 'fieldName'] as const) {
      if (distinctSelector(row, key)) {
        return { kind: 'name', name: selectorValue(chosen, key) };
      }
    }
  }
  if (distinctSelector(row, 'displayNameFromDS')) {
    return { kind: 'column', column: '__display_name__', value: selectorValue(chosen, 'displayNameFromDS') };
  }
  if (distinctSelector(row, 'fieldName')) {
    return { kind: 'column', column: '__metric_name__', value: selectorValue(chosen, 'fieldName') };
  }
  return { kind: 'refuse', reason: REASON_ALERT_SERIES_AMBIGUOUS };
}

/** `sqlExpressions` is GA and default-on from Grafana 13.1; before that an admin enables it. */
export function sqlExpressionsEnabled(cfg: { featureToggles?: { sqlExpressions?: boolean } } = config): boolean {
  return cfg?.featureToggles?.sqlExpressions === true;
}

/** A bare SQL identifier: the engine names each input table after its refId. */
const SQL_IDENTIFIER = /^[A-Za-z_][A-Za-z0-9_]*$/;

/**
 * The alert condition for a row no label or synthesized name can point at: one SQL
 * expression that selects the chosen series by name out of the metric table and the single
 * row of the baseline table, computes the percent deviation and compares it with the
 * tolerance, so the answer is a single Number (1 fires, 0 does not, NULL → NoData). Both
 * sides take the last non-null value, exactly like the chain's Reduce(`last`).
 */
export function deviationSQL(input: {
  metricRefId: string;
  baselineRefId: string;
  column: '__metric_name__' | '__display_name__';
  value: string;
  tolerance: number;
}): string {
  const tolerance =
    Number.isFinite(input.tolerance) && input.tolerance >= 0 ? input.tolerance : DEFAULT_ALERT_TOLERANCE;
  const value = input.value.replace(/'/g, "''");
  return (
    `SELECT CASE WHEN abs((m - base) / base) * 100 > ${tolerance} THEN 1 ELSE 0 END AS alert FROM ` +
    `(SELECT (SELECT a.__value__ FROM ${input.metricRefId} a WHERE a.${input.column} = '${value}'` +
    ` AND a.__value__ IS NOT NULL ORDER BY a.time DESC LIMIT 1) AS m, ` +
    `(SELECT b.__value__ FROM ${input.baselineRefId} b WHERE b.__value__ IS NOT NULL` +
    ` ORDER BY b.time DESC LIMIT 1) AS base) t`
  );
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
 * wrote, and the deviation between them. A series that can be named pairs by label (or by
 * Grafana's synthesized `__name__`) and gets the readable Reduce/Reduce/Math/Threshold
 * chain; a label-free row whose series only a column can tell apart gets a `sql` expression
 * as the condition instead. `series` is what the panel has loaded — the caller reads it
 * from the frames so a stale `alertSeries` names a series that is really there.
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

  const options = input.options ?? DEFAULT_FORECAST_OPTIONS;
  const panelDs = input.panel?.datasource;
  const rows = metricAlertTargets(input.metricTargets, panelDs, input.alertingUids);
  if (rows.length === 0) {
    return { ok: false, reason: REASON_NO_ALERTING_QUERY };
  }

  const picked = pickSeries(input.series, options.alertSeries);
  if ('reason' in picked) {
    return { ok: false, reason: picked.reason };
  }
  const chosen = picked.series;
  // The rule's metric rows must contain the chosen series: falling back to every
  // row would compare another series with this series' baseline. A frame with no
  // refId cannot be matched to a row, so it is accepted only when there is one.
  const seriesRows = chosen.refId ? rows.filter((target) => target.refId === chosen.refId) : rows;
  if (seriesRows.length === 0 || (!chosen.refId && rows.length > 1)) {
    return { ok: false, reason: REASON_ALERT_SERIES_NOT_ALERTING };
  }
  // How this rule says which series it is for. A labelled row pairs by label; a label-free
  // row is named the way the evaluation names it (Grafana's synthesized `__name__`, or a
  // SQL column the condition selects); a row nothing can name is refused, because Grafana
  // would otherwise broadcast the one baseline across every series and alert on series the
  // operator did not pick.
  const chosenRow = chosen.refId ? input.series.filter((s) => s.refId === chosen.refId) : input.series;
  const identity = seriesTarget(input.series, chosen, targetDatasourceType(chosenRow, rows, chosen, panelDs));
  if (identity.kind === 'refuse') {
    return { ok: false, reason: identity.reason };
  }

  const forecastUid = input.forecastDatasourceUid?.trim();
  if (!forecastUid) {
    return { ok: false, reason: REASON_FORECAST_DATASOURCE };
  }

  const window = windowFrom(options.alertLookback);
  const queries: AlertQuery[] = [];
  // One series is compared with one baseline, so the rule carries the metric rows that
  // produced the chosen series; the baseline row identifies its snapshot by cacheKey.
  for (const target of seriesRows) {
    // The SQL branch addresses the metric table by refId, so a target refId that is not a
    // bare SQL identifier gets a generated one — every other branch keeps the panel's.
    const declared = typeof target.refId === 'string' && target.refId ? target.refId : '';
    const refId =
      declared && (identity.kind !== 'column' || SQL_IDENTIFIER.test(declared)) ? declared : nextRefId(queries);
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

  // The overlay's key is a function of the metric targets, the panel's fit options, the
  // chosen series' display name and the visible range: the same call the panel makes, so
  // the rule restores the snapshot the overlay wrote.
  const forecastRefId = nextRefId(queries);
  const key = await cacheKey({
    targets: input.metricTargets,
    visibleFromMs: input.visibleFromMs,
    visibleToMs: input.visibleToMs,
    options,
    seriesName: chosen.name,
  });
  const seriesLabels =
    identity.kind === 'labels'
      ? chosen.labels
      : identity.kind === 'name'
        ? { __name__: identity.name }
        : undefined;
  // The query is the key and the series identity it answers for; the model and training
  // window live inside the snapshot, so no fit option is repeated here.
  const forecastQueryModel: Record<string, unknown> = {
    refId: forecastRefId,
    kind: 'forecast',
    cacheKey: key,
  };
  if (seriesLabels) {
    forecastQueryModel.seriesLabels = seriesLabels;
  }
  if (input.panel?.maxDataPoints != null) {
    forecastQueryModel.maxDataPoints = input.panel.maxDataPoints;
  }
  queries.push({
    refId: forecastRefId,
    queryType: '',
    relativeTimeRange: window,
    datasourceUid: forecastUid,
    model: forecastQueryModel,
  });

  let conditionRefId: string;
  if (identity.kind === 'column') {
    // Nothing can name this row's series for the Math node, so the condition itself selects
    // the one series and computes the deviation — the pipeline has to allow a SQL node.
    if (!sqlExpressionsEnabled()) {
      return { ok: false, reason: REASON_ALERT_SQL_OFF };
    }
    conditionRefId = nextRefId(queries);
    queries.push({
      refId: conditionRefId,
      queryType: 'expression',
      relativeTimeRange: window,
      datasourceUid: EXPRESSION_DATASOURCE_UID,
      model: {
        refId: conditionRefId,
        type: 'sql',
        datasource: EXPRESSION_DATASOURCE,
        expression: deviationSQL({
          metricRefId,
          baselineRefId: forecastRefId,
          column: identity.column,
          value: identity.value,
          tolerance: options.alertTolerance ?? DEFAULT_ALERT_TOLERANCE,
        }),
      },
    });
  } else {
    const metricReduce = nextRefId(queries);
    queries.push(reduceExpression(metricReduce, metricRefId, window));
    const baselineReduce = nextRefId(queries);
    queries.push(reduceExpression(baselineReduce, forecastRefId, window));
    const deviation = nextRefId(queries);
    queries.push(
      mathExpression(deviation, `abs(($${metricReduce} - $${baselineReduce}) / $${baselineReduce}) * 100`, window)
    );
    conditionRefId = nextRefId(queries);
    queries.push(
      thresholdExpression(conditionRefId, deviation, options.alertTolerance ?? DEFAULT_ALERT_TOLERANCE, window)
    );
  }

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
