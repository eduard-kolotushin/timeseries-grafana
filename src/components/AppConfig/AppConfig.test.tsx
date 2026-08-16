import React from 'react';
import { render, screen } from '@testing-library/react';
import { PluginType } from '@grafana/data';
import AppConfig, { AppConfigProps } from './AppConfig';

describe('Components/AppConfig', () => {
  test('shows baseline publisher fields', () => {
    const props = {
      plugin: {
        meta: {
          id: 'eduardkolotushin-forecast-app',
          name: 'Timeseries Forecast',
          type: PluginType.app,
          enabled: true,
          jsonData: {
            enabled: true,
            druidBroker: 'http://druid-broker:8082',
            druidDatasource: 'metrics',
            kafkaBrokers: 'kafka:9092',
            kafkaTopic: 'baselines',
          },
        },
      },
      query: {},
    } as unknown as AppConfigProps;

    render(<AppConfig plugin={props.plugin} query={props.query} />);
    expect(screen.getByText(/baseline publisher/i)).toBeInTheDocument();
    expect(screen.getByDisplayValue('http://druid-broker:8082')).toBeInTheDocument();
    expect(screen.getByDisplayValue('metrics')).toBeInTheDocument();
    expect(screen.getByDisplayValue('kafka:9092')).toBeInTheDocument();
    expect(screen.getByDisplayValue('baselines')).toBeInTheDocument();
  });
});
