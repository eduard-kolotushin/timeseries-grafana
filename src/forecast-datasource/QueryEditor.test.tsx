import React, { useState } from 'react';
import { DataSourceInstanceSettings } from '@grafana/data';
import { fireEvent, render, screen } from '@testing-library/react';
import { COVERAGE_SETTINGS } from '../forecast-panel/types';
import { ForecastDataSource } from './datasource';
import { QueryEditor } from './QueryEditor';
import { defaultForecastQuery, ForecastDataQuery, FORECAST_DATASOURCE_TYPE } from './types';

jest.mock('@grafana/runtime', () => ({
  ...jest.requireActual('@grafana/runtime'),
  getDataSourceSrv: () => ({ get: jest.fn() }),
}));

const settings = {
  uid: 'fc',
  name: 'Forecast',
  type: FORECAST_DATASOURCE_TYPE,
  meta: {},
  jsonData: {},
  access: 'proxy',
  readOnly: false,
} as unknown as DataSourceInstanceSettings;

const datasource = new ForecastDataSource(settings);
const key = '0123456789abcdef'.repeat(4);

function query(partial: Partial<ForecastDataQuery> = {}): ForecastDataQuery {
  return { refId: 'B', ...defaultForecastQuery, ...partial };
}

function renderEditor(q: ForecastDataQuery) {
  const onChange = jest.fn();
  const onRunQuery = jest.fn();
  // Controlled harness: the editor's invalid state derives from the query prop, so a
  // change has to come back as a new prop exactly as Grafana would deliver it.
  function Harness() {
    const [query, setQuery] = useState(q);
    return (
      <QueryEditor
        datasource={datasource}
        query={query}
        onChange={(next) => {
          onChange(next);
          setQuery(next);
        }}
        onRunQuery={onRunQuery}
      />
    );
  }
  render(<Harness />);
  return { onChange, onRunQuery };
}

describe('QueryEditor', () => {
  it('stores a pasted key and runs it once complete', () => {
    const { onChange, onRunQuery } = renderEditor(query());

    fireEvent.change(screen.getByRole('textbox', { name: 'Baseline cache key' }), { target: { value: key } });

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ cacheKey: key }));
    expect(onRunQuery).toHaveBeenCalledTimes(1);
  });

  it('stores a partial key without querying it', () => {
    const { onChange, onRunQuery } = renderEditor(query());

    fireEvent.change(screen.getByRole('textbox', { name: 'Baseline cache key' }), { target: { value: '0123' } });

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ cacheKey: '0123' }));
    // The backend refuses a malformed key, so an unfinished paste must not run.
    expect(onRunQuery).not.toHaveBeenCalled();
    expect(screen.getByRole('textbox', { name: 'Baseline cache key' })).toHaveAttribute('aria-invalid', 'true');
  });

  it('bounds Coverage with the same rule the panel option clamps to', () => {
    renderEditor(query({ kind: 'lower' }));

    const coverage = screen.getByRole('spinbutton', { name: 'Coverage' });
    expect(coverage).toHaveAttribute('min', String(COVERAGE_SETTINGS.min));
    expect(coverage).toHaveAttribute('max', String(COVERAGE_SETTINGS.max));
    expect(coverage).toHaveAttribute('step', String(COVERAGE_SETTINGS.step));
  });

  it('asks for no fit parameter: the key is the whole identity', () => {
    renderEditor(query());

    // The snapshot behind the key already holds the model and its training window.
    expect(screen.queryByRole('combobox', { name: 'Model' })).toBeNull();
    expect(screen.queryByRole('textbox', { name: 'Train from' })).toBeNull();
    expect(screen.queryByRole('textbox', { name: 'Series name' })).toBeNull();
    expect(screen.queryByText('Source query')).toBeNull();
  });
});
