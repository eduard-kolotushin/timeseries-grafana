import React from 'react';
import { DataSourcePluginOptionsEditorProps } from '@grafana/data';
import { render, screen } from '@testing-library/react';
import { ConfigEditor } from './ConfigEditor';
import { ForecastDataSourceOptions, ForecastSecureJsonData } from './types';

describe('ConfigEditor', () => {
  // A provisioned instance's jsonData can carry any key, including one the type does not declare,
  // so the rows are cast rather than type-checked into shape.
  const cases: Array<[string, ForecastDataSourceOptions | undefined, string]> = [
    [
      'lists the store keys and nothing else',
      {
        storeHost: 'pg.internal',
        storePort: 5432,
        storeDatabase: 'overlay',
        storeUser: 'overlay',
        grafanaUrl: 'http://x',
      } as unknown as ForecastDataSourceOptions,
      'storeHost, storePort, storeDatabase, storeUser',
    ],
    [
      'treats a store key of any case as one, because the backend does',
      { StoreUrl: 'postgres://overlay:overlay@pg:5432/overlay', grafanaUrl: 'http://x' } as unknown as ForecastDataSourceOptions,
      'StoreUrl',
    ],
    [
      'reads as none when the instance carries no store key',
      { grafanaUrl: 'http://x' } as unknown as ForecastDataSourceOptions,
      'none',
    ],
    ['reads as none when the instance has no jsonData at all', undefined, 'none'],
  ];

  it.each(cases)('%s', (_name, jsonData, want) => {
    const props = { options: { jsonData }, onOptionsChange: jest.fn() } as unknown as DataSourcePluginOptionsEditorProps<
      ForecastDataSourceOptions,
      ForecastSecureJsonData
    >;
    render(<ConfigEditor {...props} />);
    expect((screen.getByRole('textbox') as HTMLInputElement).value).toBe(want);
  });
});
