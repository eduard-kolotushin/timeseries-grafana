import React from 'react';
import { DataSourceInstanceSettings } from '@grafana/data';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { COVERAGE_SETTINGS } from '../forecast-panel/types';
import { ForecastDataSource } from './datasource';
import { QueryEditor } from './QueryEditor';
import { defaultForecastQuery, ForecastDataQuery, FORECAST_DATASOURCE_TYPE } from './types';

jest.mock('@grafana/runtime', () => ({
  ...jest.requireActual('@grafana/runtime'),
  DataSourcePicker: () => null,
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

function query(partial: Partial<ForecastDataQuery> = {}): ForecastDataQuery {
  return { refId: 'B', ...defaultForecastQuery, seriesName: 'up', sourceTargets: [], ...partial };
}

function renderEditor(q: ForecastDataQuery) {
  const onChange = jest.fn();
  const onRunQuery = jest.fn();
  render(<QueryEditor datasource={datasource} query={q} onChange={onChange} onRunQuery={onRunQuery} queries={[q]} />);
  return { onChange, onRunQuery };
}

describe('QueryEditor', () => {
  it('composes two train-range edits made in one tick', async () => {
    const { onChange, onRunQuery } = renderEditor(query());

    await act(async () => {
      fireEvent.change(screen.getByRole('textbox', { name: 'Train from' }), { target: { value: 'now-7d' } });
      fireEvent.change(screen.getByRole('textbox', { name: 'Train to' }), { target: { value: 'now' } });
    });

    // Neither edit may drop the other, and the row is pushed with the key for what it holds.
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        seriesName: 'up',
        trainRange: { from: 'now-7d', to: 'now' },
        cacheKey: expect.stringMatching(/^[a-f0-9]{64}$/),
      })
    );
    expect(onRunQuery).toHaveBeenCalled();
  });

  it('bounds Coverage with the same rule the panel option clamps to', () => {
    renderEditor(query({ kind: 'lower' }));

    // Unclamped, the input accepted coverage outside (0, 1) while the overlay clamped it.
    const coverage = screen.getByRole('spinbutton', { name: 'Coverage' });
    expect(coverage).toHaveAttribute('min', String(COVERAGE_SETTINGS.min));
    expect(coverage).toHaveAttribute('max', String(COVERAGE_SETTINGS.max));
    expect(coverage).toHaveAttribute('step', String(COVERAGE_SETTINGS.step));
  });

  it('carries a legacy lookback into the query the panel key is computed for', async () => {
    const { onChange } = renderEditor(query());

    fireEvent.change(screen.getByRole('textbox', { name: 'Legacy lookback' }), { target: { value: '21d' } });

    await act(async () => {});
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ lookback: '21d', cacheKey: expect.stringMatching(/^[a-f0-9]{64}$/) })
    );
  });
});
