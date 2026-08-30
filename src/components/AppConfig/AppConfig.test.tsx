import React from 'react';
import { render, screen } from '@testing-library/react';
import { PluginType } from '@grafana/data';
import AppConfig, { AppConfigProps } from './AppConfig';

describe('Components/AppConfig', () => {
  test('shows snapshot store fields', () => {
    const props = {
      plugin: {
        meta: {
          id: 'eduardkolotushin-forecast-app',
          name: 'Timeseries Forecast',
          type: PluginType.app,
          enabled: true,
          jsonData: {},
        },
      },
      query: {},
    } as unknown as AppConfigProps;

    render(<AppConfig plugin={props.plugin} query={props.query} />);
    expect(screen.getByText(/snapshot store/i)).toBeInTheDocument();
    expect(screen.getByText(/forecast\.ini\.template/)).toBeInTheDocument();
    expect(screen.getAllByText(/eduardkolotushin-forecast-app/).length).toBeGreaterThan(0);
  });
});
