import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { css, cx } from '@emotion/css';
import {
  Alert,
  Badge,
  Button,
  ClipboardButton,
  ConfirmButton,
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
import { GrafanaTheme2 } from '@grafana/data';
import { reasonFromUnknown } from '../../forecast-panel/reasons';
import {
  deleteSchedule,
  deleteScheduleModel,
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

/** The table shows minutes; the full RFC3339 timestamp stays in the cell's title. */
function shortRunTime(value: string | undefined): { text: string; title?: string } {
  if (!value) {
    return { text: '—' };
  }
  const match = /^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2})/.exec(value);
  return match ? { text: `${match[1]} ${match[2]}`, title: value } : { text: value, title: value };
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
const getStyles = (theme: GrafanaTheme2) => ({
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
  // The table keeps its own scroll area so a narrow viewport gets a scrollbar at a
  // visible height instead of at the bottom of the whole row list.
  tableWrap: css({
    '& > div': {
      maxHeight: '65vh',
      overflow: 'auto',
    },
    // Fixed layout lets the key cell clip instead of demanding the full 64-char
    // hash; the 780px floor keeps the columns readable and scrolls at tablet width.
    '& table': {
      tableLayout: 'fixed',
      width: '100%',
      minWidth: 780,
      maxWidth: 1200,
    },
    '& thead th': {
      position: 'sticky',
      top: 0,
      // Grafana's Input wrapper is positioned with z-index 1; the header must
      // outrank it or the cron/timezone fields paint over the header on scroll.
      zIndex: 10,
      background: theme.colors.background.primary,
      boxShadow: `inset 0 -1px 0 ${theme.colors.border.weak}`,
    },
  }),
  titleText: css({
    display: 'flex',
    alignItems: 'flex-start',
    gap: theme.spacing(1),
    flexWrap: 'nowrap',
  }),
  // The title shrinks and wraps; the badge keeps a fixed slot on the same line.
  titleLabel: css({
    flex: '0 1 auto',
    minWidth: 0,
    overflowWrap: 'anywhere',
  }),
  scopeBadge: css({
    flex: '0 0 auto',
    marginTop: 1,
  }),
  meta: css({
    color: theme.colors.text.secondary,
    fontSize: theme.typography.bodySmall.fontSize,
  }),
  runLine: css({
    display: 'flex',
    flexWrap: 'wrap',
    alignItems: 'center',
    gap: theme.spacing(0.5),
  }),
  srOnly: css({
    position: 'absolute',
    width: 1,
    height: 1,
    padding: 0,
    margin: -1,
    overflow: 'hidden',
    clip: 'rect(0, 0, 0, 0)',
    whiteSpace: 'nowrap',
    border: 0,
  }),
  key: css({
    display: 'block',
    flex: '1 1 auto',
    minWidth: 0,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
    fontFamily: theme.typography.fontFamilyMonospace,
    fontSize: theme.typography.bodySmall.fontSize,
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
  const [note, setNote] = useState<string | null>(null);
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
    async (row: ScheduleRow, revert?: Partial<ScheduleRow>) => {
      setBusy(rowId(row));
      try {
        await putSchedule(row);
        setError(null);
        refresh();
      } catch (e) {
        setError(reasonFromUnknown(e));
        // The optimistic patch moved the row on screen; put the server state back.
        if (revert) {
          patch(row, revert);
        }
      } finally {
        setBusy(null);
      }
    },
    [refresh, patch]
  );

  const remove = useCallback(
    async (row: ScheduleRow) => {
      setBusy(rowId(row));
      try {
        await deleteSchedule(row.scope, row.key);
        setNote(null);
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

  const removeModel = useCallback(
    async (row: ScheduleRow) => {
      setBusy(rowId(row));
      try {
        const resp = await deleteScheduleModel(row.scope, row.key);
        setNote(typeof resp?.note === 'string' ? resp.note : null);
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
        // Runs and state live here too, so the actions column does not slide off a
        // 1440px screen behind the plugin details sidebar. This column takes the
        // remaining width, so a wider window gives the title and query more room
        // instead of widening the 64-char key column.
        cell: ({ row }) => {
          const source = row.original.source;
          const name = source?.panelTitle || source?.seriesName || '';
          const href = source ? dashboardHref(source) : undefined;
          const series = source?.seriesName && source.seriesName !== name ? source.seriesName : '';
          const nextRun = shortRunTime(row.original.nextRunAt);
          const lastRun = shortRunTime(row.original.lastRunAt);
          return (
            <div className={styles.wrap} title={source?.querySummary}>
              <div className={styles.titleText}>
                <span className={styles.titleLabel}>
                  {href ? (
                    <a href={href}>{name}</a>
                  ) : (
                    <span>{name || (row.original.scope === 'baseline' ? 'Fleet-wide baseline' : '—')}</span>
                  )}
                </span>
                <span className={styles.scopeBadge}>
                  <Badge text={row.original.scope} color={row.original.scope === 'baseline' ? 'purple' : 'blue'} />
                </span>
              </div>
              {(series || source?.lookback) && (
                <div className={styles.meta}>
                  {series && <code className={styles.wrap}>{series}</code>}
                  {series && source?.lookback ? ' ' : null}
                  {source?.lookback && <span className={styles.nowrap}>{source.lookback}</span>}
                </div>
              )}
              {source?.querySummary && <span className={styles.srOnly}>{source.querySummary}</span>}
              <div className={cx(styles.meta, styles.runLine)}>
                <span className={styles.nowrap} title={nextRun.title}>
                  Next <span>{nextRun.text}</span>
                </span>
              </div>
              <div className={cx(styles.meta, styles.runLine)}>
                <span className={styles.nowrap} title={lastRun.title}>
                  Last <span>{lastRun.text}</span>
                </span>
              </div>
            </div>
          );
        },
      },
      {
        id: 'key',
        header: 'Key',
        // The whole key stays in the DOM for search and tests; the cell clips it
        // visually so a 64-char hash cannot claim a wide column.
        width: 120,
        cell: ({ row }) => (
          <Stack direction="row" gap={1} alignItems="center">
            <code className={styles.key} title={row.original.key}>
              {row.original.key}
            </code>
            <ClipboardButton
              getText={() => row.original.key}
              icon="copy"
              size="sm"
              variant="secondary"
              fill="text"
              tooltip="Copy key"
              aria-label={`${rowId(row.original)} copy key`}
            />
          </Stack>
        ),
      },
      {
        id: 'schedule',
        header: 'Schedule',
        width: 140,
        cell: ({ row }) => (
          <Stack direction="column" gap={0.5}>
            <Input
              aria-label={`${rowId(row.original)} cron`}
              width={15}
              value={row.original.cron}
              onChange={(e) => patch(row.original, { cron: e.currentTarget.value })}
            />
            <Input
              aria-label={`${rowId(row.original)} timezone`}
              width={15}
              value={row.original.timezone}
              onChange={(e) => patch(row.original, { timezone: e.currentTarget.value })}
            />
          </Stack>
        ),
      },
      {
        id: 'status',
        header: 'Status',
        width: 90,
        cell: ({ row }) => {
          const status = row.original.lastStatus?.startsWith('error')
            ? 'error'
            : row.original.lastStatus === 'ok'
              ? 'ok'
              : 'never';
          return (
            <Stack direction="column" gap={0.5} alignItems="flex-start">
              <Badge
                text={status}
                color={status === 'ok' ? 'green' : status === 'error' ? 'red' : 'orange'}
                tooltip={row.original.lastStatus || 'Never run'}
              />
              {row.original.supersededAt && (
                <Badge
                  text="Superseded"
                  color="orange"
                  tooltip={`No panel trains this key any more (since ${row.original.supersededAt}), so it is never retrained again. Delete the row to remove it.`}
                />
              )}
            </Stack>
          );
        },
      },
      {
        id: 'actions',
        header: 'Actions',
        width: 230,
        cell: ({ row }) => (
          <Stack direction="column" gap={1}>
            <Stack direction="row" gap={1} alignItems="center">
              <span className={styles.meta}>Enabled</span>
              <Switch
                aria-label={`${rowId(row.original)} enabled`}
                value={row.original.enabled}
                onChange={(e) => {
                  const enabled = e.currentTarget.checked;
                  // Show the choice at once; `commit` puts it back if the PUT rejects.
                  patch(row.original, { enabled });
                  void commit({ ...row.original, enabled }, { enabled: row.original.enabled });
                }}
              />
            </Stack>
            <Stack direction="row" gap={1} wrap="wrap">
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
              <ConfirmButton
                size="sm"
                confirmVariant="destructive"
                disabled={busy === rowId(row.original)}
                confirmText="Delete stored model"
                onConfirm={() => void removeModel(row.original)}
              >
                Delete model
              </ConfirmButton>
            </Stack>
          </Stack>
        ),
      },
    ],
    [busy, commit, patch, remove, removeModel, styles]
  );

  return (
    <div data-testid={testIds.appConfig.retrainSchedules} className={styles.body}>
      <FieldSet label="Retrain schedules">
        <p>
          One row per stored model. <code>panel</code> rows are refreshed by this Grafana backend on their cron and
          belong to this org, which is the only org that lists them; <code>baseline</code> rows are claimed by the
          baselines worker, are fleet-wide (they show in every org, and their cron, timezone and enable state are shared
          by all of them) and are created by that worker, so only an existing one can be edited here. Deleting a{' '}
          <code>baseline</code> row removes it for every org: the worker re-creates it on its next tick while the metric
          still reports, which is how a retired hash&apos;s row stops being retrained.
        </p>
        <p>
          A <code>panel</code> row whose panel no longer trains its key — the training window or the query changed — is
          marked <em>Superseded</em>. The panel keeps writing the row that matches what it trains now; this one is kept
          for its history and is never claimed again, so delete it to remove it.
        </p>
        <p>
          A <code>baseline</code> row&apos;s key is the upstream <code>metric_hash</code> — the only identity this store
          keeps for it — so copy the key to match a row against a metric, and use Search to find a <code>panel</code>{' '}
          row by its dashboard panel, series or query.
        </p>
        {error && (
          <div data-testid={testIds.appConfig.retrainError}>
            <Alert title="Schedules failed" severity="error">
              {error}
            </Alert>
          </div>
        )}
        {note && (
          <Alert title="Model removal" severity="info">
            {note}
          </Alert>
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
        <div className={styles.tableWrap}>
          <InteractiveTable columns={columns} data={pageRows} getRowId={rowId} pageSize={0} />
        </div>
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
