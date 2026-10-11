import React from 'react';
import { DataFrame, FieldType, toDataFrame } from '@grafana/data';
import { render } from '@testing-library/react';
import { AlertSeriesEditor } from './AlertSeriesEditor';
import { ForecastOptions } from './types';

jest.mock('@grafana/runtime', () => ({
  ...jest.requireActual('@grafana/runtime'),
  locationService: { getSearchObject: () => ({ editPanel: 1 }) },
}));

const options: ForecastOptions = {
  model: 'holt',
  alpha: 0.8,
  beta: 0.2,
  period: 7,
  season: 'hour',
  calendar: '',
  showInterval: true,
  interval: 0.95,
  trainRange: { from: '', to: '' },
  forecastRange: { from: '', to: '' },
};

function frame(names: string[]): DataFrame {
  return toDataFrame({
    refId: 'A',
    fields: [
      { name: 'Time', type: FieldType.time, values: [0, 1000] },
      ...names.map((name) => ({ name, type: FieldType.number, values: [1, 2] })),
    ],
  });
}

function renderEditor(data: DataFrame[], value?: string) {
  const onChange = jest.fn();
  const utils = render(
    <AlertSeriesEditor
      value={value as never}
      onChange={onChange}
      context={{ data, options } as never}
      item={undefined as never}
    />
  );
  return { onChange, ...utils };
}

describe('AlertSeriesEditor', () => {
  // A `Select` renders its first option even while the panel option is empty, so the
  // placeholder has to be an option of its own — otherwise an unchosen series is displayed
  // as if it had been picked.
  it('shows the placeholder and writes nothing while the choice is empty', () => {
    const { onChange, getByText } = renderEditor([frame(['A-series', 'A-series1'])]);
    expect(getByText('Select a series')).toBeDefined();
    expect(onChange).not.toHaveBeenCalled();
  });

  it('renders a stored choice', () => {
    const { onChange, queryByText } = renderEditor([frame(['A-series', 'A-series1'])], 'A-series1');
    expect(queryByText('Select a series')).toBeNull();
    expect(onChange).not.toHaveBeenCalled();
  });

  it('renders nothing when the panel has a single series', () => {
    const { container, onChange } = renderEditor([frame(['only'])]);
    expect(container.innerHTML).toBe('');
    expect(onChange).not.toHaveBeenCalled();
  });
});
