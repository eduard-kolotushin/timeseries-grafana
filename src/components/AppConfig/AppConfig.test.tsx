import React from 'react';
import { render, screen } from '@testing-library/react';
import { PluginType } from '@grafana/data';
import AppConfig, { AppConfigProps } from './AppConfig';

describe('Components/AppConfig', () => {
  test('explains that the forecast panel needs no API settings', () => {
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
    expect(screen.queryByText(/no extra api settings/i)).toBeInTheDocument();
  });
});
