import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { AppRootProps, PluginType } from '@grafana/data';
import { render, waitFor } from '@testing-library/react';
import App from './App';

describe('Components/App', () => {
  test('renders without an error', async () => {
    const props = {
      basename: 'a/eduardkolotushin-forecast-app',
      meta: {
        id: 'eduardkolotushin-forecast-app',
        name: 'Timeseries Forecast',
        type: PluginType.app,
        enabled: true,
        jsonData: {},
      },
      query: {},
      path: '',
      onNavChanged: jest.fn(),
    } as unknown as AppRootProps;

    const { queryByText } = render(
      <MemoryRouter>
        <App {...props} />
      </MemoryRouter>
    );

    await waitFor(
      () => expect(queryByText(/forecast overlay visualization/i)).toBeInTheDocument(),
      { timeout: 2000 }
    );
  });
});
