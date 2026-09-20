import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { css } from '@emotion/css';
import {
  Alert,
  Button,
  Field,
  FieldSet,
  FilterInput,
  Input,
  InteractiveTable,
  Pagination,
  RadioButtonGroup,
  Stack,
  Switch,
  useStyles2,
} from '@grafana/ui';
import { reasonFromUnknown } from '../../forecast-panel/reasons';
import { deleteSchedule, listSchedules, putSchedule, ScheduleRow } from '../../forecast-panel/scheduleApi';
import { testIds } from '../testIds';

const rowId = (row: ScheduleRow) => `${row.scope}/${row.key}`;

/**
 * Rows per page. Paging is client-side: `GET /schedules` returns every row for the
 * org in one response and a row is ~200 bytes, so a few thousand rows stay a single
 * small fetch.
 */
const PAGE_SIZE = 20;

type ScopeFilter = 'all' | ScheduleRow['scope'];
type EnabledFilter = 'all' | 'enabled' | 'disabled';
type StatusFilter = 'all' | 'ok' | 'error' | 'never';

const scopeOptions: Array<{ label: string; value: ScopeFilter }> = [
  { label: 'All', value: 'all' },
  { label: 'panel', value: 'panel' },
  { label: 'baseline', value: 'baseline' },
];

const enabledOptions: Array<{ label: string; value: EnabledFilter }> = [
  { label: 'All', value: 'all' },
  { label: 'Enabled', value: 'enabled' },
  { label: 'Disabled', value: 'disabled' },
];

const statusOptions: Array<{ label: string; value: StatusFilter }> = [
  { label: 'All', value: 'all' },
  { label: 'ok', value: 'ok' },
  { label: 'error', value: 'error' },
  { label: 'never run', value: 'never' },
];

/** `last_status` is `ok` or `error: …` (the scheduler's Finish call); empty means never run. */
function matchesStatus(row: ScheduleRow, status: StatusFilter): boolean {
  switch (status) {
    case 'all':
      return true;
    case 'ok':
      return row.lastStatus === 'ok';
    case 'error':
      return row.lastStatus?.startsWith('error') ?? false;
    case 'never':
      return !row.lastStatus;
  }
}

type ScheduleColumns = React.ComponentProps<typeof InteractiveTable<ScheduleRow>>['columns'];

/**
 * Cell text uses the `anywhere` overflow-wrap value, which caps every cell's
 * min-content width, so a 64-char cache key or a long retrain error wraps instead of
 * forcing the table (and the plugin details row it sits in) wider than the viewport
 * side panel allows.
 */
const getStyles = () => ({
  body: css({
    // Grafana's plugin details page sizes its content column at min-content, so a wide
    // table pushes the 250px sidebar off screen. Size containment makes this tab's width
    // independent of the table's intrinsic width; the table then scrolls inside
    // InteractiveTable's own overflow container instead of pushing the sidebar out.
    contain: 'inline-size',
  }),
  // Grafana's global stylesheet pins `code` to nowrap, so a 64-char cache key needs both
  // a normal white-space and a break-anywhere rule to wrap inside its cell. `anywhere`
  // (not `break-word`) is what lets the column's min-content width collapse.
  wrap: css({
    whiteSpace: 'normal',
    overflowWrap: 'anywhere',
  }),
  // A table cell ignores `min-width` in auto layout, so the RFC3339 timestamps stay on
  // one line by being unbreakable content instead of by a column minimum.
  nowrap: css({
    whiteSpace: 'nowrap',
  }),
});

/**
 * `forecast.retrain` rows served by the app plugin backend, on the Retrain schedules
 * app config page. Admin-only: a non-admin sees the 403 body in the error alert
 * instead of a table.
 */
export const RetrainSchedules = () => {
  const styles = useStyles2(getStyles);
  const [rows, setRows] = useState<ScheduleRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [scope, setScope] = useState<ScopeFilter>('all');
  const [enabled, setEnabled] = useState<EnabledFilter>('all');
  const [status, setStatus] = useState<StatusFilter>('all');
  const [page, setPage] = useState(1);

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

  const patch = useCallback((row: ScheduleRow, next: Partial<ScheduleRow>) => {
    setRows((prev) => prev.map((r) => (rowId(r) === rowId(row) ? { ...r, ...next } : r)));
  }, []);

  const commit = useCallback(
    async (row: ScheduleRow) => {
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
    },
    [load]
  );

  const remove = useCallback(
    async (row: ScheduleRow) => {
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
    },
    [load]
  );

  const filtered = useMemo(() => {
    const needle = search.trim().toLowerCase();
    return rows.filter(
      (row) =>
        (scope === 'all' || row.scope === scope) &&
        (enabled === 'all' || row.enabled === (enabled === 'enabled')) &&
        matchesStatus(row, status) &&
        (needle === '' || row.key.toLowerCase().includes(needle))
    );
  }, [rows, scope, enabled, status, search]);

  const pageCount = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE));
  // The result set shrinks under the current page (delete, filter), so clamp instead of storing the page.
  const currentPage = Math.min(page, pageCount);
  const pageRows = useMemo(
    () => filtered.slice((currentPage - 1) * PAGE_SIZE, currentPage * PAGE_SIZE),
    [filtered, currentPage]
  );

  const columns = useMemo<ScheduleColumns>(
    () => [
      { id: 'scope', header: 'Scope', cell: ({ row }) => row.original.scope },
      {
        id: 'key',
        header: 'Key',
        // A panel key is a 64-char cache hash: give it room to wrap onto two lines instead
        // of a one-character-wide sliver. Table width containment keeps this from pushing
        // the plugin details sidebar out; the table scrolls inside its own container.
        minWidth: 260,
        cell: ({ row }) => <code className={styles.wrap}>{row.original.key}</code>,
      },
      {
        id: 'cron',
        header: 'Cron',
        width: 180,
        cell: ({ row }) => (
          <Input
            aria-label={`${rowId(row.original)} cron`}
            width={15}
            value={row.original.cron}
            onChange={(e) => patch(row.original, { cron: e.currentTarget.value })}
          />
        ),
      },
      {
        id: 'timezone',
        header: 'Timezone',
        width: 180,
        cell: ({ row }) => (
          <Input
            aria-label={`${rowId(row.original)} timezone`}
            width={15}
            value={row.original.timezone}
            onChange={(e) => patch(row.original, { timezone: e.currentTarget.value })}
          />
        ),
      },
      {
        id: 'nextRunAt',
        header: 'Next run',
        cell: ({ row }) => <span className={styles.nowrap}>{row.original.nextRunAt || '—'}</span>,
      },
      {
        id: 'lastRunAt',
        header: 'Last run',
        cell: ({ row }) => <span className={styles.nowrap}>{row.original.lastRunAt || '—'}</span>,
      },
      { id: 'lastStatus', header: 'Status', cell: ({ row }) => row.original.lastStatus ?? '—' },
      {
        id: 'enabled',
        header: 'Enabled',
        width: 90,
        cell: ({ row }) => (
          <Switch
            aria-label={`${rowId(row.original)} enabled`}
            value={row.original.enabled}
            onChange={(e) => void commit({ ...row.original, enabled: e.currentTarget.checked })}
          />
        ),
      },
      {
        id: 'actions',
        header: '',
        cell: ({ row }) => (
          <Stack direction="row" gap={1}>
            <Button
              size="sm"
              variant="secondary"
              disabled={busy === rowId(row.original)}
              aria-label={`${rowId(row.original)} save`}
              onClick={() => void commit(row.original)}
            >
              Save
            </Button>
            {row.original.scope === 'panel' && (
              <Button
                size="sm"
                variant="destructive"
                disabled={busy === rowId(row.original)}
                aria-label={`${rowId(row.original)} delete`}
                onClick={() => void remove(row.original)}
              >
                Delete
              </Button>
            )}
          </Stack>
        ),
      },
    ],
    [busy, commit, patch, remove, styles]
  );

  return (
    <div data-testid={testIds.appConfig.retrainSchedules} className={styles.body}>
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
        <Stack direction="row" gap={2} wrap="wrap" alignItems="flex-end">
          <Field label="Search">
            <FilterInput
              escapeRegex={false}
              value={search}
              placeholder="key"
              width={40}
              onChange={(value) => {
                setSearch(value);
                setPage(1);
              }}
            />
          </Field>
          <Field label="Scope">
            <RadioButtonGroup
              options={scopeOptions}
              value={scope}
              onChange={(value) => {
                setScope(value);
                setPage(1);
              }}
            />
          </Field>
          <Field label="Enabled">
            <RadioButtonGroup
              options={enabledOptions}
              value={enabled}
              onChange={(value) => {
                setEnabled(value);
                setPage(1);
              }}
            />
          </Field>
          <Field label="Status">
            <RadioButtonGroup
              options={statusOptions}
              value={status}
              onChange={(value) => {
                setStatus(value);
                setPage(1);
              }}
            />
          </Field>
        </Stack>
        <InteractiveTable columns={columns} data={pageRows} getRowId={rowId} pageSize={0} />
        <Pagination currentPage={currentPage} numberOfPages={pageCount} onNavigate={setPage} hideWhenSinglePage />
        {loading && <p>Loading schedules…</p>}
        {!loading && rows.length === 0 && <p>No schedules yet. Train the overlay once to create one.</p>}
        {!loading && rows.length > 0 && filtered.length === 0 && <p>No schedules match these filters.</p>}
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
    </div>
  );
};

export default RetrainSchedules;
