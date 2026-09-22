import React, { useEffect, useRef, useState } from 'react';
import { DataQuery, DataSourceApi, QueryEditorProps, SelectableValue } from '@grafana/data';
import { DataSourcePicker, getDataSourceSrv } from '@grafana/runtime';
import { Button, Field, InlineField, InlineFieldRow, Input, Select } from '@grafana/ui';
import { ForecastDataSource } from './datasource';
import { COVERAGE_SETTINGS } from '../forecast-panel/types';
import {
  copySourceFromSibling,
  innerSourceQuery,
  siblingMetricQueries,
  sourceDatasource,
  withCacheKey,
  withSourceTarget,
} from './queryModel';
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

export function QueryEditor({ query, onChange, onRunQuery, queries }: Props) {
  // The resolved instance is kept together with the uid it belongs to, so the
  // editor the render picks is derived instead of mirrored: clearing it when the
  // uid goes away is a render-time fact, not a setState inside an effect.
  const [resolvedDs, setResolvedDs] = useState<{ uid: string; ds: DataSourceApi | null } | null>(null);
  const dsRef = sourceDatasource(query);
  const dsUid = dsRef?.uid;

  useEffect(() => {
    if (!dsUid) {
      return;
    }
    let cancelled = false;
    getDataSourceSrv()
      .get(dsUid)
      .then((ds) => {
        if (!cancelled) {
          setResolvedDs({ uid: dsUid, ds });
        }
      })
      .catch(() => {
        if (!cancelled) {
          setResolvedDs({ uid: dsUid, ds: null });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [dsUid]);

  const sourceDs = dsUid && resolvedDs?.uid === dsUid ? resolvedDs.ds : null;

  // The query the editor last produced, so `update` composes instead of spreading a
  // render-time prop (see below), and the serial number that makes only the newest
  // in-flight `withCacheKey` flush.
  const latest = useRef(query);
  useEffect(() => {
    latest.current = query;
  }, [query]);
  const updateSeq = useRef(0);

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

  /** A whole-query or partial change, or a function of the query the editor holds now. */
  const update = (
    patch: Partial<ForecastDataQuery> | ((query: ForecastDataQuery) => Partial<ForecastDataQuery>)
  ) => {
    // Compose on top of the last query this editor produced. Spreading the render-time
    // prop instead would drop an earlier edit made in the same tick, because both flushes
    // would start from the same object and the later one would land without the earlier patch.
    const base = latest.current;
    const composed = { ...base, ...(typeof patch === 'function' ? patch(base) : patch) };
    latest.current = composed;
    const seq = ++updateSeq.current;
    void withCacheKey(composed).then((next) => {
      if (seq !== updateSeq.current) {
        // A newer edit already composed this one and pushes its own key.
        return;
      }
      latest.current = next;
      onChange(next);
      onRunQuery();
    });
  };

  const SourceEditor = sourceDs?.components?.QueryEditor;
  const inner = innerSourceQuery(query);
  const siblings = siblingMetricQueries(queries as DataQuery[] | undefined, query.refId);
  const sourceSibling = siblings.find((q) => q.refId === 'A') ?? siblings[0];

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
              min={COVERAGE_SETTINGS.min}
              max={COVERAGE_SETTINGS.max}
              step={COVERAGE_SETTINGS.step}
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
              update((q) => ({ trainRange: { ...q.trainRange, from: e.currentTarget.value } }))
            }
          />
        </InlineField>
        <InlineField label="Train to">
          <Input
            width={16}
            value={query.trainRange?.to ?? ''}
            placeholder="Auto"
            onChange={(e) => update((q) => ({ trainRange: { ...q.trainRange, to: e.currentTarget.value } }))}
          />
        </InlineField>
        <InlineField
          label="Legacy lookback"
          tooltip="Duration lookback (e.g. 21d, or a bare day count) from before the training-period picker. Must match the overlay panel’s Legacy lookback to share its cacheKey. Empty is Auto."
        >
          <Input
            width={16}
            value={query.lookback ?? ''}
            placeholder="Auto"
            onChange={(e) => update({ lookback: e.currentTarget.value })}
          />
        </InlineField>
      </InlineFieldRow>
      <Field
        label="Source query"
        description="Same datasource and SQL/expr as overlay query A. Time macros (${__from}) do not need to match interpolated panel timestamps. Auto train from/to must stay Auto if the overlay used Auto. Used only for the cacheKey fingerprint; this plugin does not run it."
      >
        <>
          {sourceSibling && (
            <div style={{ marginBottom: 8 }}>
              <Button
                variant="secondary"
                size="sm"
                type="button"
                onClick={() => update((q) => copySourceFromSibling(q, sourceSibling))}
              >
                Copy source from query {sourceSibling.refId || 'A'}
              </Button>
            </div>
          )}
          <DataSourcePicker
            noDefault
            current={dsRef?.uid}
            filter={(ds) => ds.type !== FORECAST_DATASOURCE_TYPE}
            onChange={(ds) =>
              update((q) => {
                const innerQ = innerSourceQuery(q);
                return withSourceTarget(q, { uid: ds.uid, type: ds.type }, {
                  ...(innerQ as unknown as Record<string, unknown>),
                  refId: innerQ.refId || 'A',
                });
              })
            }
          />
        </>
      </Field>
      {SourceEditor && sourceDs && (
        <SourceEditor
          datasource={sourceDs}
          query={inner}
          onRunQuery={onRunQuery}
          onChange={(q) =>
            update((current) =>
              withSourceTarget(
                current,
                { uid: sourceDs.uid, type: sourceDs.type },
                q as unknown as Record<string, unknown>
              )
            )
          }
        />
      )}
    </div>
  );
}
