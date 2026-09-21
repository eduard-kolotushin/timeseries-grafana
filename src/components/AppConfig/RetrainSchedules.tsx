import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { css } from '@emotion/css';
import {
  Alert,
  Button,
  ClipboardButton,
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
import { config } from '@grafana/runtime';
import { reasonFromUnknown } from '../../forecast-panel/reasons';
import {
  deleteSchedule,
  listSchedules,
  putSchedule,
  ScheduleRow,
  ScheduleSource,
} from '../../forecast-panel/scheduleApi';
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

/** Deep link to the dashboard panel that trained a row; absent without a dashboard. */
function dashboardHref(source: ScheduleSource): string | undefined {
  if (!source.dashboardUid || !source.panelId) {
    return undefined;
  }
  return `${config.appSubUrl}/d/${encodeURIComponent(source.dashboardUid)}?viewPanel=${source.panelId}`;
}

/** Search covers what identifies a row, not only the cache hash the table keys on. */
function searchHaystack(row: ScheduleRow): string {
  return [row.key, row.source?.panelTitle, row.source?.seriesName, row.source?.querySummary]
    .filter((part): part is string => Boolean(part))
    .join(' ')
    .toLowerCase();
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

  // Every fetch is triggered by bumping `reload`, so the effect body only starts
  // the request and never calls a function that sets state synchronously.
  const [reload, setReload] = useState(0);

  const refresh = useCallback(() => {
    setLoading(true);
    setReload((n) => n + 1);
  }, []);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const next = await listSchedules();
        if (!cancelled) {
          setRows(next);
          setError(null);
        }
      } catch (e) {
        if (!cancelled) {
          setError(reasonFromUnknown(e));
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [reload]);

  const patch = useCallback((row: ScheduleRow, next: Partial<ScheduleRow>) => {
    setRows((prev) => prev.map((r) => (rowId(r) === rowId(row) ? { ...r, ...next } : r)));
  }, []);

  const commit = useCallback(
    async (row: ScheduleRow) => {
      setBusy(rowId(row));
      try {
        await putSchedule(row);
        setError(null);
        refresh();
      } catch (e) {
        setError(reasonFromUnknown(e));
      } finally {
        setBusy(null);
      }
    },
    [refresh]
  );

  const remove = useCallback(
    async (row: ScheduleRow) => {
      setBusy(rowId(row));
      try {
        await deleteSchedule(row.scope, row.key);
        setError(null);
        refresh();
      } catch (e) {
        setError(reasonFromUnknown(e));
      } finally {
        setBusy(null);
      }
    },
    [refresh]
  );

  const filtered = useMemo(() => {
    const needle = search.trim().toLowerCase();
    return rows.filter(
      (row) =>
        (scope === 'all' || row.scope === scope) &&
        (enabled === 'all' || row.enabled === (enabled === 'enabled')) &&
        matchesStatus(row, status) &&
        (needle === '' || searchHaystack(row).includes(needle))
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
      {
        id: 'source',
        header: 'Source',
        // A panel row is identified by the dashboard panel that trained it: the title
        // links to that panel, and the series plus query say which of its targets this
        // row refits. A baseline row has none of this — its key is the metric hash.
        minWidth: 260,
        cell: ({ row }) => {
          const source = row.original.source;
          const name = source?.panelTitle || source?.seriesName || '';
          if (!source || !name) {
            return <span className={styles.nowrap}>—</span>;
          }
          const href = dashboardHref(source);
          const series = source.seriesName && source.seriesName !== name ? source.seriesName : '';
          return (
            <div className={styles.wrap}>
              {href ? <a href={href}>{name}</a> : <span>{name}</span>}
              {(series || source.lookback || source.querySummary) && (
                <div>
                  {series && <code className={styles.wrap}>{series}</code>}
                  {series && (source.lookback || source.querySummary) ? ' ' : null}
                  {source.lookback && <span className={styles.nowrap}>{source.lookback}</span>}
                  {source.lookback && source.querySummary ? ' ' : null}
                  {source.querySummary && <span title={source.querySummary}>{source.querySummary}</span>}
                </div>
              )}
            </div>
          );
        },
      },
      { id: 'scope', header: 'Scope', cell: ({ row }) => row.original.scope },
      {
        id: 'key',
        header: 'Key',
        // A panel key is a 64-char cache hash: give it room to wrap onto two lines instead
        // of a one-character-wide sliver. Table width containment keeps this from pushing
        // the plugin details sidebar out; the table scrolls inside its own container.
        minWidth: 260,
        cell: ({ row }) => (
          <Stack direction="row" gap={1} alignItems="center">
            <code className={styles.wrap}>{row.original.key}</code>
            <ClipboardButton
              getText={() => row.original.key}
              icon="copy"
              size="sm"
              tooltip="Copy key"
              aria-label={`${rowId(row.original)} copy key`}
            />
          </Stack>
        ),
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
            <Button
              size="sm"
              variant="destructive"
              disabled={busy === rowId(row.original)}
              aria-label={`${rowId(row.original)} delete`}
              onClick={() => void remove(row.original)}
            >
              Delete
            </Button>
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
          One row per stored model. <code>panel</code> rows are refreshed by this Grafana backend on their cron and
          belong to this org, which is the only org that lists them; <code>baseline</code> rows are claimed by the
          baselines worker, are fleet-wide (they show in every org, and their cron, timezone and enable state are
          shared by all of them) and are created by that worker, so only an existing one can be edited here. Deleting
          a <code>baseline</code> row removes it for every org: the worker re-creates it on its next tick while the
          metric still reports, which is how a retired hash&apos;s row stops being retrained.
        </p>
        <p>
          A <code>baseline</code> row&apos;s key is the upstream <code>metric_hash</code> — the only identity this
          store keeps for it — so copy the key to match a row against a metric, and use Search to find a{' '}
          <code>panel</code> row by its dashboard panel, series or query.
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
              placeholder="key, panel, series"
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
          onClick={refresh}
        >
          Refresh
        </Button>
      </FieldSet>
    </div>
  );
};

export default RetrainSchedules;
