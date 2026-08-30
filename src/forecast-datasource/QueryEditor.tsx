import React, { useEffect, useState } from 'react';
import { DataSourceApi, QueryEditorProps, SelectableValue } from '@grafana/data';
import { DataSourcePicker, getDataSourceSrv } from '@grafana/runtime';
import { Field, InlineField, InlineFieldRow, Input, Select } from '@grafana/ui';
import { ForecastDataSource } from './datasource';
import { innerSourceQuery, sourceDatasource, withCacheKey, withSourceTarget } from './queryModel';
import {
  FORECAST_DATASOURCE_TYPE,
  ForecastDataQuery,
  ForecastDataSourceOptions,
  ForecastOutputKind,
} from './types';

type Props = QueryEditorProps<ForecastDataSource, ForecastDataQuery, ForecastDataSourceOptions>;

const KIND_OPTIONS: Array<SelectableValue<ForecastOutputKind>> = [
  { value: 'forecast', label: 'Forecast' },
  { value: 'lower', label: 'Interval lower' },
  { value: 'upper', label: 'Interval upper' },
];

const MODEL_OPTIONS: Array<SelectableValue<ForecastDataQuery['model']>> = [
  { value: 'naive', label: 'Naive' },
  { value: 'mean', label: 'Mean' },
  { value: 'drift', label: 'Drift' },
  { value: 'seasonal', label: 'Seasonal naive' },
  { value: 'baseline', label: 'Seasonal baseline' },
  { value: 'ses', label: 'SES' },
  { value: 'holt', label: 'Holt' },
];

const SEASON_OPTIONS: Array<SelectableValue<ForecastDataQuery['season']>> = [
  { value: 'hour', label: 'Hour' },
  { value: 'day', label: 'Day' },
  { value: 'week', label: 'Week (hour of week)' },
  { value: 'minute-week', label: 'Week (minute of week)' },
];

const CALENDAR_OPTIONS: Array<SelectableValue<ForecastDataQuery['calendar']>> = [
  { value: '', label: 'Off' },
  { value: 'ru', label: 'RU' },
];

export function QueryEditor({ query, onChange, onRunQuery }: Props) {
  const [sourceDs, setSourceDs] = useState<DataSourceApi | null>(null);
  const dsRef = sourceDatasource(query);

  useEffect(() => {
    let cancelled = false;
    if (!dsRef?.uid) {
      setSourceDs(null);
      return;
    }
    getDataSourceSrv()
      .get(dsRef.uid)
      .then((ds) => {
        if (!cancelled) {
          setSourceDs(ds);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setSourceDs(null);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [dsRef?.uid]);

  const fingerprint = [
    query.seriesName,
    query.model,
    query.alpha,
    query.beta,
    query.period,
    query.season,
    query.calendar,
    query.trainRange?.from,
    query.trainRange?.to,
    query.lookback,
    JSON.stringify(query.sourceTargets ?? []),
  ].join('\0');

  useEffect(() => {
    let cancelled = false;
    void withCacheKey(query).then((next) => {
      if (!cancelled && next.cacheKey !== query.cacheKey) {
        onChange(next);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [fingerprint, query, onChange]);

  const update = (patch: Partial<ForecastDataQuery>) => {
    void withCacheKey({ ...query, ...patch }).then((next) => {
      onChange(next);
      onRunQuery();
    });
  };

  const SourceEditor = sourceDs?.components?.QueryEditor;
  const inner = innerSourceQuery(query);

  return (
    <div>
      <InlineFieldRow>
        <InlineField label="Output" tooltip="One series per Grafana query row (refId). Add another query for lower or upper.">
          <Select
            width={20}
            options={KIND_OPTIONS}
            value={query.kind ?? 'forecast'}
            onChange={(v) => update({ kind: v.value ?? 'forecast' })}
          />
        </InlineField>
        <InlineField
          label="Series name"
          tooltip="Must match the overlay series name (Grafana display name used in the cacheKey)."
          grow
        >
          <Input
            value={query.seriesName ?? ''}
            placeholder="Same as overlay series"
            onChange={(e) => update({ seriesName: e.currentTarget.value })}
          />
        </InlineField>
      </InlineFieldRow>
      <InlineFieldRow>
        <InlineField label="Model">
          <Select
            width={20}
            options={MODEL_OPTIONS}
            value={query.model ?? 'holt'}
            onChange={(v) => update({ model: v.value ?? 'holt' })}
          />
        </InlineField>
        {(query.model === 'ses' || query.model === 'holt') && (
          <InlineField label="Alpha">
            <Input
              type="number"
              width={8}
              value={query.alpha ?? 0.8}
              onChange={(e) => update({ alpha: Number(e.currentTarget.value) })}
            />
          </InlineField>
        )}
        {query.model === 'holt' && (
          <InlineField label="Beta">
            <Input
              type="number"
              width={8}
              value={query.beta ?? 0.2}
              onChange={(e) => update({ beta: Number(e.currentTarget.value) })}
            />
          </InlineField>
        )}
        {query.model === 'seasonal' && (
          <InlineField label="Period">
            <Input
              type="number"
              width={8}
              value={query.period ?? 7}
              onChange={(e) => update({ period: Number(e.currentTarget.value) })}
            />
          </InlineField>
        )}
        {query.model === 'baseline' && (
          <>
            <InlineField label="Seasonality">
              <Select
                width={22}
                options={SEASON_OPTIONS}
                value={query.season ?? 'hour'}
                onChange={(v) => update({ season: v.value ?? 'hour' })}
              />
            </InlineField>
            <InlineField label="Calendar">
              <Select
                width={12}
                options={CALENDAR_OPTIONS}
                value={query.calendar ?? ''}
                onChange={(v) => update({ calendar: v.value ?? '' })}
              />
            </InlineField>
          </>
        )}
        {(query.kind === 'lower' || query.kind === 'upper') && (
          <InlineField label="Coverage" tooltip="Same interval coverage as the overlay (default 0.95).">
            <Input
              type="number"
              width={8}
              value={query.level ?? 0.95}
              onChange={(e) => update({ level: Number(e.currentTarget.value) })}
            />
          </InlineField>
        )}
      </InlineFieldRow>
      <InlineFieldRow>
        <InlineField
          label="Train from"
          tooltip="Must match the overlay training-period strings (empty is Auto)."
        >
          <Input
            width={16}
            value={query.trainRange?.from ?? ''}
            placeholder="Auto"
            onChange={(e) =>
              update({ trainRange: { from: e.currentTarget.value, to: query.trainRange?.to ?? '' } })
            }
          />
        </InlineField>
        <InlineField label="Train to">
          <Input
            width={16}
            value={query.trainRange?.to ?? ''}
            placeholder="Auto"
            onChange={(e) =>
              update({ trainRange: { from: query.trainRange?.from ?? '', to: e.currentTarget.value } })
            }
          />
        </InlineField>
      </InlineFieldRow>
      <Field
        label="Source query"
        description="Same datasource and SQL/expr as overlay query A. Time macros (${__from}) do not need to match interpolated panel timestamps. Auto train from/to must stay Auto if the overlay used Auto. Used only for the cacheKey fingerprint; this plugin does not run it."
      >
        <DataSourcePicker
          noDefault
          current={dsRef?.uid}
          filter={(ds) => ds.type !== FORECAST_DATASOURCE_TYPE}
          onChange={(ds) => {
            const innerQ = innerSourceQuery(query);
            update(
              withSourceTarget(query, { uid: ds.uid, type: ds.type }, {
                ...(innerQ as unknown as Record<string, unknown>),
                refId: innerQ.refId || 'A',
              })
            );
          }}
        />
      </Field>
      {SourceEditor && sourceDs && (
        <SourceEditor
          datasource={sourceDs}
          query={inner}
          onRunQuery={onRunQuery}
          onChange={(q) => {
            update(
              withSourceTarget(
                query,
                { uid: sourceDs.uid, type: sourceDs.type },
                q as unknown as Record<string, unknown>
              )
            );
          }}
        />
      )}
    </div>
  );
}
