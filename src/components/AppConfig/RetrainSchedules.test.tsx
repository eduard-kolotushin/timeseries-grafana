import React from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { ScheduleRow } from '../../forecast-panel/scheduleApi';
import { testIds } from '../testIds';
import RetrainSchedules from './RetrainSchedules';

const mockGet = jest.fn();
const mockPut = jest.fn();
const mockDelete = jest.fn();

jest.mock('@grafana/runtime', () => ({
  getBackendSrv: () => ({ get: mockGet, put: mockPut, delete: mockDelete }),
  config: { appSubUrl: '/grafana' },
}));

const panelRow: ScheduleRow = {
  scope: 'panel',
  key: 'panelhash',
  cron: '0 3 * * *',
  timezone: 'UTC',
  enabled: true,
  nextRunAt: '2026-09-21T03:00:00Z',
  lastRunAt: '2026-09-20T03:00:00Z',
  lastStatus: 'ok',
  hasSpec: true,
};

const baselineRow: ScheduleRow = {
  scope: 'baseline',
  key: 'baselinehash',
  cron: '*/5 * * * *',
  timezone: 'UTC',
  enabled: true,
  nextRunAt: '2026-09-20T12:05:00Z',
  lastRunAt: '2026-09-20T12:00:00Z',
  lastStatus: 'ok',
};

const errorRow: ScheduleRow = {
  ...panelRow,
  key: 'errorhash',
  lastStatus: 'error: dial tcp: connection refused',
};

const neverRow: ScheduleRow = { ...panelRow, key: 'neverhash', lastStatus: undefined };

/** A row whose panel moved to another training window, so nothing writes this key any more. */
const supersededRow: ScheduleRow = { ...panelRow, key: 'supersededhash', supersededAt: '2026-09-21T04:00:00Z' };

/** A row trained by a panel that recorded where it lives and what it queried. */
const sourcedRow: ScheduleRow = {
  ...panelRow,
  key: 'sourcedhash',
  source: {
    dashboardUid: 'dash-1',
    panelId: 7,
    panelTitle: 'CPU',
    datasourceUid: 'prom',
    seriesName: 'up{job="api"}',
    querySummary: 'PromQL: sum(rate(http_requests_total[5m]))',
    lookback: '21d',
  },
};

const manyRows = (count: number): ScheduleRow[] =>
  Array.from({ length: count }, (_, i) => ({ ...panelRow, key: `panelhash${i}` }));

function renderSchedules(rows: ScheduleRow[]) {
  mockGet.mockResolvedValue(rows);
  return render(<RetrainSchedules />);
}

/** The table has no per-row testid; an input's aria-label identifies its row. */
function rowOf(ariaLabel: string): HTMLElement {
  const tr = screen.getByLabelText(ariaLabel).closest('tr');
  if (!tr) {
    throw new Error(`no row for ${ariaLabel}`);
  }
  return tr;
}

const pickRadio = (legend: string, option: string) => {
  fireEvent.click(within(screen.getByRole('group', { name: legend })).getByRole('radio', { name: option }));
};

describe('RetrainSchedules', () => {
  beforeEach(() => {
    mockGet.mockReset();
    mockPut.mockReset();
    mockDelete.mockReset();
  });

  it('renders one row per schedule with its next and last run', async () => {
    renderSchedules([panelRow, baselineRow]);
    expect(await screen.findByText('panelhash')).toBeInTheDocument();
    expect(screen.getByText('baselinehash')).toBeInTheDocument();
    const panel = rowOf('panel/panelhash cron');
    expect(within(panel).getByText('2026-09-21T03:00:00Z')).toBeInTheDocument();
    expect(within(panel).getByText('2026-09-20T03:00:00Z')).toBeInTheDocument();
    expect(screen.queryByTestId(testIds.appConfig.retrainError)).toBeNull();
  });

  it('pages long lists and drops the pager when a filter narrows them to one page', async () => {
    renderSchedules(manyRows(25));
    await screen.findByLabelText('panel/panelhash0 cron');
    expect(screen.queryByLabelText('panel/panelhash20 cron')).toBeNull();
    fireEvent.click(within(screen.getByRole('navigation')).getByRole('button', { name: '2' }));
    expect(await screen.findByLabelText('panel/panelhash20 cron')).toBeInTheDocument();
    expect(within(screen.getByRole('navigation')).getByRole('button', { name: '1' })).toBeInTheDocument();
    fireEvent.change(screen.getByRole('textbox', { name: 'Search' }), { target: { value: 'panelhash2' } });
    expect(await screen.findByLabelText('panel/panelhash2 cron')).toBeInTheDocument();
    expect(screen.queryByRole('navigation')).toBeNull();
  });

  it('hides the pager for a single page', async () => {
    renderSchedules([panelRow, baselineRow]);
    await screen.findByLabelText('panel/panelhash cron');
    expect(screen.queryByRole('navigation')).toBeNull();
  });

  it('filters by key search', async () => {
    renderSchedules([panelRow, baselineRow]);
    await screen.findByLabelText('panel/panelhash cron');
    fireEvent.change(screen.getByRole('textbox', { name: 'Search' }), { target: { value: 'BASELINE' } });
    expect(screen.queryByLabelText('panel/panelhash cron')).toBeNull();
    expect(screen.getByLabelText('baseline/baselinehash cron')).toBeInTheDocument();
  });

  it('filters by scope', async () => {
    renderSchedules([panelRow, baselineRow]);
    await screen.findByLabelText('panel/panelhash cron');
    pickRadio('Scope', 'baseline');
    expect(screen.queryByLabelText('panel/panelhash cron')).toBeNull();
    expect(screen.getByLabelText('baseline/baselinehash cron')).toBeInTheDocument();
    pickRadio('Scope', 'panel');
    expect(screen.queryByLabelText('baseline/baselinehash cron')).toBeNull();
    expect(screen.getByLabelText('panel/panelhash cron')).toBeInTheDocument();
  });

  it('filters by enabled state', async () => {
    renderSchedules([panelRow, { ...panelRow, key: 'disabled', enabled: false }]);
    await screen.findByLabelText('panel/panelhash cron');
    pickRadio('Enabled', 'Disabled');
    expect(screen.queryByLabelText('panel/panelhash cron')).toBeNull();
    expect(screen.getByLabelText('panel/disabled cron')).toBeInTheDocument();
  });

  it('filters by last status, treating a missing status as never run', async () => {
    renderSchedules([panelRow, errorRow, neverRow]);
    await screen.findByLabelText('panel/panelhash cron');
    pickRadio('Status', 'error');
    expect(screen.queryByLabelText('panel/panelhash cron')).toBeNull();
    expect(screen.getByLabelText('panel/errorhash cron')).toBeInTheDocument();
    pickRadio('Status', 'never run');
    expect(screen.getByLabelText('panel/neverhash cron')).toBeInTheDocument();
    expect(screen.queryByLabelText('panel/errorhash cron')).toBeNull();
  });

  it.each<[string, ScheduleRow, number]>([
    ['marks a superseded row so it can be found and deleted', supersededRow, 1],
    ['leaves a row its panel still trains unmarked', panelRow, 0],
  ])('%s', async (_name, row, badges) => {
    renderSchedules([row]);
    await screen.findByLabelText(`panel/${row.key} cron`);
    expect(within(rowOf(`panel/${row.key} cron`)).queryAllByText('Superseded')).toHaveLength(badges);
  });

  it('deletes a superseded row off its cron', async () => {
    renderSchedules([supersededRow, panelRow]);
    await screen.findByLabelText('panel/supersededhash cron');
    fireEvent.click(screen.getByLabelText('panel/supersededhash delete'));
    await waitFor(() => expect(mockDelete).toHaveBeenCalledTimes(1));
    expect(mockDelete.mock.calls[0][0]).toContain('scope=panel&key=supersededhash');
  });

  it('saves an edited cron', async () => {
    renderSchedules([panelRow]);
    const cron = await screen.findByLabelText('panel/panelhash cron');
    fireEvent.change(cron, { target: { value: '*/2 * * * *' } });
    fireEvent.click(screen.getByLabelText('panel/panelhash save'));
    await waitFor(() => expect(mockPut).toHaveBeenCalledTimes(1));
    expect(mockPut.mock.calls[0][1]).toEqual({ ...panelRow, cron: '*/2 * * * *' });
  });

  it('persists a disabled toggle', async () => {
    renderSchedules([panelRow]);
    fireEvent.click(await screen.findByLabelText('panel/panelhash enabled'));
    await waitFor(() => expect(mockPut).toHaveBeenCalledTimes(1));
    expect(mockPut.mock.calls[0][1]).toEqual({ ...panelRow, enabled: false });
  });

  it('surfaces a 403 from a failed save', async () => {
    mockPut.mockRejectedValue({ status: 403, data: 'forecast: admin required\n' });
    renderSchedules([panelRow]);
    fireEvent.click(await screen.findByLabelText('panel/panelhash save'));
    expect(await screen.findByText('forecast: admin required')).toBeInTheDocument();
    expect(screen.getByTestId(testIds.appConfig.retrainError)).toBeInTheDocument();
  });

  it('identifies a panel row by its dashboard panel, series and query', async () => {
    renderSchedules([sourcedRow, baselineRow]);
    const link = await screen.findByRole('link', { name: 'CPU' });
    // The link is what turns a cache hash back into "the panel whose CPU chart this is".
    expect(link.getAttribute('href')).toBe('/grafana/d/dash-1?viewPanel=7');
    const row = rowOf('panel/sourcedhash cron');
    expect(within(row).getByText('up{job="api"}')).toBeInTheDocument();
    expect(within(row).getByText('PromQL: sum(rate(http_requests_total[5m]))')).toBeInTheDocument();
    expect(within(row).getByText('21d')).toBeInTheDocument();
  });

  it('shows the scope per row and copies a key', async () => {
    renderSchedules([sourcedRow, baselineRow]);
    await screen.findByLabelText('panel/sourcedhash cron');
    expect(within(rowOf('panel/sourcedhash cron')).getByText('panel')).toBeInTheDocument();
    const baseline = rowOf('baseline/baselinehash cron');
    expect(within(baseline).getByText('baseline')).toBeInTheDocument();
    // A worker baseline row has no spec, so it has no source but keeps its hash.
    expect(within(baseline).queryByRole('link')).toBeNull();
    expect(screen.getByLabelText('baseline/baselinehash copy key')).toBeInTheDocument();
  });

  it('finds a row by the panel, series or query it belongs to', async () => {
    renderSchedules([sourcedRow, panelRow]);
    await screen.findByLabelText('panel/sourcedhash cron');
    fireEvent.change(screen.getByRole('textbox', { name: 'Search' }), { target: { value: 'cpu' } });
    // The hash the table keys on does not contain this text; the source does.
    expect(screen.getByLabelText('panel/sourcedhash cron')).toBeInTheDocument();
    expect(screen.queryByLabelText('panel/panelhash cron')).toBeNull();
    fireEvent.change(screen.getByRole('textbox', { name: 'Search' }), { target: { value: 'http_requests_total' } });
    expect(screen.getByLabelText('panel/sourcedhash cron')).toBeInTheDocument();
  });

  it('deletes a row of either scope', async () => {
    renderSchedules([panelRow, baselineRow]);
    await screen.findByText('panelhash');
    // A baseline row is fleet-wide and the worker re-creates a live hash's row, so
    // deleting one is how a retired hash's row stops being retrained forever.
    fireEvent.click(screen.getByLabelText('baseline/baselinehash delete'));
    await waitFor(() => expect(mockDelete).toHaveBeenCalledTimes(1));
    expect(mockDelete.mock.calls[0][0]).toContain('scope=baseline&key=baselinehash');
    fireEvent.click(screen.getByLabelText('panel/panelhash delete'));
    await waitFor(() => expect(mockDelete).toHaveBeenCalledTimes(2));
    expect(mockDelete.mock.calls[1][0]).toContain('scope=panel&key=panelhash');
  });
});
