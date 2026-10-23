import React from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2, QueryEditorProps, SelectableValue } from '@grafana/data';
import { InlineField, InlineFieldRow, Input, Select, useStyles2 } from '@grafana/ui';
import { ForecastDataSource } from './datasource';
import { COVERAGE_SETTINGS } from '../forecast-panel/types';
import { CACHE_KEY_PATTERN, ForecastDataQuery, ForecastDataSourceOptions, ForecastOutputKind } from './types';

type Props = QueryEditorProps<ForecastDataSource, ForecastDataQuery, ForecastDataSourceOptions>;

const KIND_OPTIONS: Array<SelectableValue<ForecastOutputKind>> = [
  { value: 'forecast', label: 'Forecast' },
  { value: 'lower', label: 'Interval lower' },
  { value: 'upper', label: 'Interval upper' },
];

/**
 * A trained baseline is identified by its cache key alone: the snapshot already holds the
 * model and the training window, so this editor never asks for either. The key comes from
 * the overlay panel (Training → Baseline keys) or from New alert rule, which fills it in.
 */
const getStyles = (theme: GrafanaTheme2) => ({
  keyInput: css({
    '& input': {
      fontFamily: theme.typography.fontFamilyMonospace,
    },
  }),
  hint: css({
    color: theme.colors.text.secondary,
    fontSize: theme.typography.bodySmall.fontSize,
    margin: theme.spacing(1, 0, 0),
  }),
});

export function QueryEditor({ query, onChange, onRunQuery }: Props) {
  const styles = useStyles2(getStyles);
  const key = query.cacheKey ?? '';
  const invalid = key !== '' && !CACHE_KEY_PATTERN.test(key);

  const update = (patch: Partial<ForecastDataQuery>, run = true) => {
    onChange({ ...query, ...patch });
    if (run) {
      onRunQuery();
    }
  };

  return (
    <div>
      <InlineFieldRow>
        <InlineField
          label="Output"
          tooltip="One series per Grafana query row (refId). Add another query for lower or upper."
        >
          <Select
            width={20}
            options={KIND_OPTIONS}
            value={query.kind ?? 'forecast'}
            onChange={(v) => update({ kind: v.value ?? 'forecast' })}
          />
        </InlineField>
        <InlineField
          label="Baseline cache key"
          tooltip="The trained baseline to restore. Copy it from the overlay panel's Training → Baseline keys, or let New alert rule fill it in."
          invalid={invalid}
          error={invalid ? 'Expected 64 lowercase hex characters.' : undefined}
          grow
        >
          <Input
            invalid={invalid}
            value={key}
            placeholder="64 lowercase hex characters"
            className={styles.keyInput}
            onChange={(e) => {
              const next = e.currentTarget.value.trim();
              // A partial key is stored while typing but never queried: the backend
              // refuses a malformed one, and only a complete key can restore a snapshot.
              update({ cacheKey: next }, next === '' || CACHE_KEY_PATTERN.test(next));
            }}
          />
        </InlineField>
        {(query.kind === 'lower' || query.kind === 'upper') && (
          <InlineField label="Coverage" tooltip="Prediction-band coverage of the lower/upper bound (default 0.95).">
            <Input
              type="number"
              width={8}
              min={COVERAGE_SETTINGS.min}
              max={COVERAGE_SETTINGS.max}
              step={COVERAGE_SETTINGS.step}
              value={query.level ?? 0.95}
              onChange={(e) => update({ level: Number(e.currentTarget.value) })}
            />
          </InlineField>
        )}
      </InlineFieldRow>
      {key === '' && (
        <p className={styles.hint}>
          No baseline selected. Copy a key from the overlay panel&apos;s <strong>Training → Baseline keys</strong>, or
          start from <strong>New alert rule</strong>.
        </p>
      )}
    </div>
  );
}
