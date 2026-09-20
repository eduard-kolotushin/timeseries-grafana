import React from 'react';
import { render, screen } from '@testing-library/react';
import { PluginType } from '@grafana/data';
import { testIds } from '../testIds';
import AppConfig, { AppConfigProps } from './AppConfig';

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

describe('Components/AppConfig', () => {
  test('shows snapshot store fields', () => {
    render(<AppConfig plugin={props.plugin} query={props.query} />);
    expect(screen.getByText(/snapshot store/i)).toBeInTheDocument();
    expect(screen.getByText(/default retrain schedule/i)).toBeInTheDocument();
    expect(screen.getByText(/forecast\.ini\.template/)).toBeInTheDocument();
    expect(screen.getAllByText(/eduardkolotushin-forecast-app/).length).toBeGreaterThan(0);
  });

  test('does not render the schedule table, which lives on its own tab', () => {
    render(<AppConfig plugin={props.plugin} query={props.query} />);
    expect(screen.queryByTestId(testIds.appConfig.retrainSchedules)).toBeNull();
    expect(screen.queryByTestId(testIds.appConfig.retrainRefresh)).toBeNull();
  });
});
