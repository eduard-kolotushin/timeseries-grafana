import { getBackendSrv } from '@grafana/runtime';
import { APP_PLUGIN_ID } from '../constants';

/** Admin-gated retrain-schedule resources in the app plugin backend. */
export const SCHEDULE_RESOURCE = `/api/plugins/${APP_PLUGIN_ID}/resources/schedules`;
export const SCHEDULE_DEFAULT_RESOURCE = `${SCHEDULE_RESOURCE}/default`;

export type ScheduleScope = 'panel' | 'baseline';

/** One `forecast.retrain` row. The backend never echoes `spec`; only `hasSpec` leaks. */
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
