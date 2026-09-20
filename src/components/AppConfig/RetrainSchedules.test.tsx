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

const onDefaultCronChange = jest.fn();
const onDefaultTimezoneChange = jest.fn();

function renderSchedules(rows: ScheduleRow[]) {
  mockGet.mockResolvedValue(rows);
  return render(
    <RetrainSchedules
      defaultCron="0 3 * * *"
      defaultTimezone="UTC"
      onDefaultCronChange={onDefaultCronChange}
      onDefaultTimezoneChange={onDefaultTimezoneChange}
    />
  );
}

describe('RetrainSchedules', () => {
  beforeEach(() => {
    mockGet.mockReset();
    mockPut.mockReset();
    mockDelete.mockReset();
    onDefaultCronChange.mockReset();
    onDefaultTimezoneChange.mockReset();
  });

  it('renders one row per schedule with its next and last run', async () => {
    renderSchedules([panelRow, baselineRow]);
    expect(await screen.findByText('panelhash')).toBeInTheDocument();
    expect(screen.getByText('baselinehash')).toBeInTheDocument();
    expect(screen.getAllByTestId(testIds.appConfig.retrainRow)).toHaveLength(2);
    const panel = screen.getAllByTestId(testIds.appConfig.retrainRow)[0];
    expect(within(panel).getByText('2026-09-21T03:00:00Z')).toBeInTheDocument();
    expect(within(panel).getByText('2026-09-20T03:00:00Z')).toBeInTheDocument();
    expect(screen.queryByTestId(testIds.appConfig.retrainError)).toBeNull();
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

  it('deletes panel rows only', async () => {
    renderSchedules([panelRow, baselineRow]);
    await screen.findByText('panelhash');
    expect(screen.queryByLabelText('baseline/baselinehash delete')).toBeNull();
    fireEvent.click(screen.getByLabelText('panel/panelhash delete'));
    await waitFor(() => expect(mockDelete).toHaveBeenCalledTimes(1));
    expect(mockDelete.mock.calls[0][0]).toContain('scope=panel&key=panelhash');
  });

  it('hands the default schedule inputs back to the settings form', async () => {
    renderSchedules([]);
    fireEvent.change(await screen.findByLabelText('Default retrain cron'), { target: { value: '*/7 * * * *' } });
    fireEvent.change(screen.getByLabelText('Default retrain timezone'), { target: { value: 'Europe/Moscow' } });
    expect(onDefaultCronChange).toHaveBeenCalledWith('*/7 * * * *');
    expect(onDefaultTimezoneChange).toHaveBeenCalledWith('Europe/Moscow');
  });
});
