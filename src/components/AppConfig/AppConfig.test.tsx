import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { PluginType } from '@grafana/data';
import { APP_PLUGIN_ID } from '../../constants';
import { SCHEDULE_DEFAULT_RESOURCE } from '../../forecast-panel/scheduleApi';
import { testIds } from '../testIds';
import AppConfig, { AppConfigProps } from './AppConfig';

const mockPost = jest.fn();

jest.mock('@grafana/runtime', () => ({
  getBackendSrv: () => ({ post: mockPost }),
}));

const propsWith = (jsonData: Record<string, unknown>) =>
  ({
    plugin: {
      meta: {
        id: APP_PLUGIN_ID,
        name: 'Timeseries Forecast',
        type: PluginType.app,
        enabled: true,
        pinned: false,
        jsonData,
      },
    },
    query: {},
  }) as unknown as AppConfigProps;

const props = propsWith({});

describe('Components/AppConfig', () => {
  beforeEach(() => {
    mockPost.mockReset();
  });

  test('has no store fields and no store explanation, only the retrain schedule', () => {
    render(<AppConfig plugin={props.plugin} query={props.query} />);
    expect(screen.getByText(/default retrain schedule/i)).toBeInTheDocument();
    expect(screen.getAllByText(/eduardkolotushin-forecast-app/).length).toBeGreaterThan(0);
    // The store is deployment configuration: the page offers no fields for it and does not explain it either.
    expect(screen.queryByText(/snapshot store/i)).toBeNull();
    expect(screen.queryByText(/deployment parameter/i)).toBeNull();
    expect(screen.getByRole('textbox', { name: /^Cron/ })).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: /^Timezone/ })).toBeInTheDocument();
    for (const label of [/^Host/, /^Port/, /^Database/, /^User/, /^SSL mode/, /^Password/]) {
      expect(screen.queryByRole('textbox', { name: label })).toBeNull();
    }
  });

  test('does not render the schedule table, which lives on its own tab', () => {
    render(<AppConfig plugin={props.plugin} query={props.query} />);
    expect(screen.queryByTestId(testIds.appConfig.retrainSchedules)).toBeNull();
    expect(screen.queryByTestId(testIds.appConfig.retrainRefresh)).toBeNull();
  });

  test('keeps the Save button below every field it saves', () => {
    render(<AppConfig plugin={props.plugin} query={props.query} />);
    const save = screen.getByRole('button', { name: 'Save' });
    const timezone = screen.getByRole('textbox', { name: /^Timezone/ });
    const notes = screen.getByText(/Plugin id:/);
    expect(timezone.compareDocumentPosition(save) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(notes.compareDocumentPosition(save) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  test('validates the default schedule and leaves jsonData it does not render untouched', async () => {
    mockPost.mockResolvedValue({});
    const plugin = propsWith({
      retrainEnabled: false,
      grafanaUrl: 'https://grafana.internal',
      retrainCron: '*/5 * * * *',
      storeHost: 'pg.internal',
      storeDatabase: 'overlay',
    });
    render(<AppConfig plugin={plugin.plugin} query={plugin.query} />);
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(2));

    const [validateUrl, validateBody] = mockPost.mock.calls[0];
    expect(validateUrl).toBe(SCHEDULE_DEFAULT_RESOURCE);
    expect(validateBody).toEqual({ cron: '*/5 * * * *', timezone: 'UTC' });

    const [settingsUrl, settingsBody] = mockPost.mock.calls[1];
    expect(settingsUrl).toBe(`/api/plugins/${APP_PLUGIN_ID}/settings`);
    // A save writes only the retrain default: the provisioned store keys survive verbatim and the
    // secure store password is not sent at all, so the page can never clear it.
    expect(settingsBody.jsonData).toEqual({
      retrainEnabled: false,
      grafanaUrl: 'https://grafana.internal',
      storeHost: 'pg.internal',
      storeDatabase: 'overlay',
      retrainCron: '*/5 * * * *',
      retrainTimezone: 'UTC',
    });
    expect(settingsBody.secureJsonData).toBeUndefined();
    expect(screen.queryByText(/Settings not saved/)).toBeNull();
  });

  test('shows a rejected default schedule and saves nothing', async () => {
    mockPost.mockRejectedValueOnce({ status: 400, data: 'forecast: invalid cron expression\n' });
    render(<AppConfig plugin={props.plugin} query={props.query} />);
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('forecast: invalid cron expression')).toBeInTheDocument();
    expect(screen.getByText(/Settings not saved/)).toBeInTheDocument();
    expect(mockPost).toHaveBeenCalledTimes(1);
  });

  test('still saves when the default schedule cannot be validated', async () => {
    // A plugins:write user without the Admin role the schedule API requires must keep
    // being able to save the default schedule.
    mockPost.mockRejectedValueOnce({ status: 403, data: 'forecast: admin required\n' });
    mockPost.mockResolvedValueOnce({});
    render(<AppConfig plugin={props.plugin} query={props.query} />);
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(2));
    expect(mockPost.mock.calls[1][0]).toBe(`/api/plugins/${APP_PLUGIN_ID}/settings`);
    expect(screen.queryByText(/Settings not saved/)).toBeNull();
  });
});
