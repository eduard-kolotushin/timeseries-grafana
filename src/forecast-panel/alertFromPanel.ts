export const EXPRESSION_DATASOURCE_UID = '__expr__';
export const EXPRESSION_DATASOURCE_TYPE = '__expr__';

export const REASON_DASHBOARD_NOT_SAVED = 'Dashboard must be saved before alerts can be added.';
export const REASON_NO_ALERTING_QUERY =
  'Cannot create alerts from this panel because no query to an alerting capable datasource is found.';

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

export const DEFAULT_RELATIVE_TIME_RANGE: RelativeTimeRange = { from: 600, to: 0 };

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

function defaultExpressions(reduceRefId: string, thresholdRefId: string, sourceRefId: string): AlertQuery[] {
  const exprDs = { uid: EXPRESSION_DATASOURCE_UID, type: EXPRESSION_DATASOURCE_TYPE };
  return [
    {
      refId: reduceRefId,
      datasourceUid: EXPRESSION_DATASOURCE_UID,
      queryType: 'expression',
      relativeTimeRange: DEFAULT_RELATIVE_TIME_RANGE,
      model: {
        refId: reduceRefId,
        type: 'reduce',
        datasource: exprDs,
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
    },
    {
      refId: thresholdRefId,
      datasourceUid: EXPRESSION_DATASOURCE_UID,
      queryType: 'expression',
      relativeTimeRange: DEFAULT_RELATIVE_TIME_RANGE,
      model: {
        refId: thresholdRefId,
        type: 'threshold',
        datasource: exprDs,
        expression: reduceRefId,
        conditions: [
          {
            type: 'query',
            evaluator: { params: [0], type: 'gt' },
            operator: { type: 'and' },
            query: { params: [thresholdRefId] },
            reducer: { params: [], type: 'last' },
          },
        ],
      },
    },
  ];
}

export function ruleFormDefaultsFromPanel(input: {
  dashboardUid?: string;
  panel?: LivePanel;
  panelId?: number;
  relativeTimeRange?: RelativeTimeRange;
  alertingUids: Set<string>;
}): AlertFromPanelResult {
  const dashboardUid = input.dashboardUid?.trim();
  if (!dashboardUid) {
    return { ok: false, reason: REASON_DASHBOARD_NOT_SAVED };
  }
  const panelId = input.panelId ?? input.panel?.id;
  if (panelId == null || !Number.isFinite(Number(panelId))) {
    return { ok: false, reason: REASON_DASHBOARD_NOT_SAVED };
  }

  const relativeTimeRange = input.relativeTimeRange ?? DEFAULT_RELATIVE_TIME_RANGE;
  const panelDs = input.panel?.datasource;
  const queries: AlertQuery[] = [];
  for (const target of input.panel?.targets ?? []) {
    if (target.hide) {
      continue;
    }
    const uid = datasourceUid(target.datasource, panelDs);
    if (!uid || !input.alertingUids.has(uid)) {
      continue;
    }
    const refId = typeof target.refId === 'string' && target.refId ? target.refId : nextRefId(queries);
    const { hide: _hide, ...model } = target;
    queries.push({
      refId,
      queryType: target.queryType ?? '',
      relativeTimeRange,
      datasourceUid: uid,
      model: { ...model, refId, maxDataPoints: input.panel?.maxDataPoints },
    });
  }

  if (!queries.length || !queries.some((q) => q.datasourceUid !== EXPRESSION_DATASOURCE_UID)) {
    return { ok: false, reason: REASON_NO_ALERTING_QUERY };
  }

  if (!queries.some((q) => q.datasourceUid === EXPRESSION_DATASOURCE_UID)) {
    const lastData = [...queries].reverse().find((q) => q.datasourceUid !== EXPRESSION_DATASOURCE_UID);
    const reduceRefId = nextRefId(queries);
    const thresholdRefId = nextRefId([...queries, { refId: reduceRefId }]);
    queries.push(...defaultExpressions(reduceRefId, thresholdRefId, lastData?.refId ?? 'A'));
  }

  return {
    ok: true,
    defaults: {
      type: 'grafana-alerting',
      name: input.panel?.title || 'New alert rule',
      queries,
      condition: queries[queries.length - 1].refId,
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
