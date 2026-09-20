import React, { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Field, FieldSet, Input, Switch } from '@grafana/ui';
import { reasonFromUnknown } from '../../forecast-panel/reasons';
import { deleteSchedule, listSchedules, putSchedule, ScheduleRow } from '../../forecast-panel/scheduleApi';
import { testIds } from '../testIds';

export type RetrainSchedulesProps = {
  /** Default schedule; persisted by AppConfig's settings POST, not from here. */
  defaultCron: string;
  defaultTimezone: string;
  onDefaultCronChange: (cron: string) => void;
  onDefaultTimezoneChange: (timezone: string) => void;
};

const rowId = (row: ScheduleRow) => `${row.scope}/${row.key}`;

/**
 * `forecast.retrain` rows served by the app plugin backend. Admin-only: a non-admin
 * sees the 403 body in the error alert instead of a table.
 */
export const RetrainSchedules = ({
  defaultCron,
  defaultTimezone,
  onDefaultCronChange,
  onDefaultTimezoneChange,
}: RetrainSchedulesProps) => {
  const [rows, setRows] = useState<ScheduleRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setRows(await listSchedules());
      setError(null);
    } catch (e) {
      setError(reasonFromUnknown(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const patch = (row: ScheduleRow, next: Partial<ScheduleRow>) => {
    setRows((prev) => prev.map((r) => (rowId(r) === rowId(row) ? { ...r, ...next } : r)));
  };

  const commit = async (row: ScheduleRow) => {
    setBusy(rowId(row));
    try {
      await putSchedule(row);
      setError(null);
      await load();
    } catch (e) {
      setError(reasonFromUnknown(e));
    } finally {
      setBusy(null);
    }
  };

  const remove = async (row: ScheduleRow) => {
    setBusy(rowId(row));
    try {
      await deleteSchedule(row.scope, row.key);
      setError(null);
      await load();
    } catch (e) {
      setError(reasonFromUnknown(e));
    } finally {
      setBusy(null);
    }
  };

  return (
    <div data-testid={testIds.appConfig.retrainSchedules}>
      <FieldSet label="Retrain schedules">
        <p>
          One row per stored model. <code>panel</code> rows are refreshed by this Grafana backend on their cron;
          <code>baseline</code> rows are claimed by the baselines worker and cannot be deleted here.
        </p>
        {error && (
          <div data-testid={testIds.appConfig.retrainError}>
            <Alert title="Schedules failed" severity="error">
              {error}
            </Alert>
          </div>
        )}
        <div style={{ overflowX: 'auto' }}>
          <table style={{ minWidth: 960, borderCollapse: 'collapse' }}>
            <thead>
              <tr>
                <th align="left">Scope</th>
                <th align="left">Key</th>
                <th align="left">Cron</th>
                <th align="left">Timezone</th>
                <th align="left">Next run</th>
                <th align="left">Last run</th>
                <th align="left">Status</th>
                <th align="left">Enabled</th>
                <th align="left" />
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => {
                const id = rowId(row);
                return (
                  <tr key={id} data-testid={testIds.appConfig.retrainRow}>
                    <td>{row.scope}</td>
                    <td>
                      <code>{row.key}</code>
                    </td>
                    <td>
                      {/* A cron expression is ~11 chars and a clipped field is unreadable: give
                          both time fields to the fields themselves, not to the table's auto width. */}
                      <Input
                        aria-label={`${id} cron`}
                        width={18}
                        value={row.cron}
                        onChange={(e) => patch(row, { cron: e.currentTarget.value })}
                      />
                    </td>
                    <td>
                      <Input
                        aria-label={`${id} timezone`}
                        width={18}
                        value={row.timezone}
                        onChange={(e) => patch(row, { timezone: e.currentTarget.value })}
                      />
                    </td>
                    <td>{row.nextRunAt || '—'}</td>
                    <td>{row.lastRunAt || '—'}</td>
                    <td>{row.lastStatus ?? '—'}</td>
                    <td>
                      <Switch
                        aria-label={`${id} enabled`}
                        value={row.enabled}
                        onChange={(e) => void commit({ ...row, enabled: e.currentTarget.checked })}
                      />
                    </td>
                    <td>
                      <Button
                        size="sm"
                        variant="secondary"
                        disabled={busy === id}
                        aria-label={`${id} save`}
                        onClick={() => void commit(row)}
                      >
                        Save
                      </Button>
                      {row.scope === 'panel' && (
                        <Button
                          size="sm"
                          variant="destructive"
                          disabled={busy === id}
                          aria-label={`${id} delete`}
                          onClick={() => void remove(row)}
                        >
                          Delete
                        </Button>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        {loading && <p>Loading schedules…</p>}
        {!loading && rows.length === 0 && <p>No schedules yet. Train the overlay once to create one.</p>}
        <Button
          size="sm"
          variant="secondary"
          fill="outline"
          data-testid={testIds.appConfig.retrainRefresh}
          disabled={loading}
          onClick={() => void load()}
        >
          Refresh
        </Button>
      </FieldSet>
      <FieldSet label="Default retrain schedule">
        <p>
          Written to <code>jsonData.retrainCron</code> / <code>jsonData.retrainTimezone</code> by the Save button above
          the table. Used for every model without a row of its own.
        </p>
        <Field label="Cron" description="5-field cron, or @hourly / @daily. The backend parses it per row.">
          <Input
            aria-label="Default retrain cron"
            value={defaultCron}
            onChange={(e) => onDefaultCronChange(e.currentTarget.value)}
          />
        </Field>
        <Field label="Timezone" description="IANA zone, e.g. UTC or Europe/Moscow.">
          <Input
            aria-label="Default retrain timezone"
            value={defaultTimezone}
            onChange={(e) => onDefaultTimezoneChange(e.currentTarget.value)}
          />
        </Field>
      </FieldSet>
    </div>
  );
};

export default RetrainSchedules;
