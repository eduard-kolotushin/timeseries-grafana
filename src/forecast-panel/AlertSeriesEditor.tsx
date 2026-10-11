import React from 'react';
import { StandardEditorProps } from '@grafana/data';
import { Select } from '@grafana/ui';
import { livePanel, panelIdFromLocation, panelSeries } from './alertFromPanel';
import { ForecastOptions } from './types';

/**
 * The metric series the alert rule is for. Rendered only when the panel has more than one:
 * with a single series there is nothing to choose, and the rule uses it. The names are the
 * ones the fit uses (`getFieldDisplayName`), so the choice matches the trained snapshot.
 */
export const AlertSeriesEditor: React.FC<StandardEditorProps<string, unknown, ForecastOptions>> = ({
  value,
  onChange,
  context,
}) => {
  const panel = livePanel(panelIdFromLocation()).panel;
  const names: string[] = [];
  for (const series of panelSeries(context.data, panel?.targets)) {
    if (!names.includes(series.name)) {
      names.push(series.name);
    }
  }
  if (names.length < 2) {
    return null;
  }
  return (
    <Select
      options={names.map((name) => ({ label: name, value: name }))}
      value={value || undefined}
      onChange={(option) => onChange(option?.value)}
      placeholder="Select a series"
      width={30}
    />
  );
};
