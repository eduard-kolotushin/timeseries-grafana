import { getBackendSrv } from '@grafana/runtime';
import { APP_PLUGIN_ID } from '../constants';

/** Admin-gated retrain-schedule resources in the app plugin backend. */
export const SCHEDULE_RESOURCE = `/api/plugins/${APP_PLUGIN_ID}/resources/schedules`;
export const SCHEDULE_DEFAULT_RESOURCE = `${SCHEDULE_RESOURCE}/default`;

export type ScheduleScope = 'panel' | 'baseline';

/** What a row belongs to, derived by the backend from the stored spec. */
export type ScheduleSource = {
  dashboardUid?: string;
  panelId?: number;
  panelTitle?: string;
  datasourceUid?: string;
  seriesName?: string;
  querySummary?: string;
  lookback?: string;
};

/**
 * One `forecast.retrain` row. The backend never echoes `spec`; it sends `hasSpec`
 * and the derived `source` (absent for a worker baseline row, which has no spec).
 */
export type ScheduleRow = {
  scope: ScheduleScope;
  key: string;
  cron: string;
  timezone: string;
  enabled: boolean;
  nextRunAt?: string;
  lastRunAt?: string;
  lastStatus?: string;
  hasSpec?: boolean;
  source?: ScheduleSource;
};

export function listSchedules(): Promise<ScheduleRow[]> {
  return getBackendSrv().get<ScheduleRow[]>(SCHEDULE_RESOURCE);
}

export function putSchedule(row: ScheduleRow): Promise<unknown> {
  return getBackendSrv().put(SCHEDULE_RESOURCE, row);
}

export function deleteSchedule(scope: string, key: string): Promise<unknown> {
  const query = `scope=${encodeURIComponent(scope)}&key=${encodeURIComponent(key)}`;
  return getBackendSrv().delete(`${SCHEDULE_RESOURCE}?${query}`);
}

export function postScheduleDefault(def: { cron: string; timezone: string }): Promise<unknown> {
  return getBackendSrv().post(SCHEDULE_DEFAULT_RESOURCE, def);
}
