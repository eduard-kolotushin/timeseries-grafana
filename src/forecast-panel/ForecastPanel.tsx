import React, { useEffect, useMemo, useState } from 'react';
import {
  applyFieldOverrides,
  DataFrame,
  dateTime,
  FieldType,
  MutableDataFrame,
  PanelProps,
} from '@grafana/data';
import { getBackendSrv, PanelDataErrorView } from '@grafana/runtime';
import { LegendDisplayMode, TooltipDisplayMode } from '@grafana/schema';
import { Alert, TimeSeries, TooltipPlugin, useTheme2 } from '@grafana/ui';
import { FORECAST_RESOURCE } from '../constants';
import { extractSeries, pickTrainingPoints } from './extract';
import { forecastLevel, resolveTrainWindow } from './lookback';
import { queryTrainingFrames } from './trainQuery';
import { ForecastOptions, ForecastResponse } from './types';

interface Props extends PanelProps<ForecastOptions> {}

export const ForecastPanel: React.FC<Props> = ({
  options,
  data,
  width,
  height,
  timeRange,
  timeZone,
  fieldConfig,
  replaceVariables,
  id,
}) => {
  const theme = useTheme2();
  const [frames, setFrames] = useState<DataFrame[]>(data.series);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      setError(null);
      const history: DataFrame[] = [];
      const forecasts: DataFrame[] = [];
      const { fromMs: trainFromMs, toMs: trainToMs } = resolveTrainWindow(
        options,
        timeRange.to.valueOf(),
        timeZone
      );
      let trainFrames: DataFrame[] | null = null;
      try {
        trainFrames = await queryTrainingFrames(data.request, trainFromMs, trainToMs);
      } catch (e) {
        if (!cancelled) {
          setError(e instanceof Error ? e.message : String(e));
        }
      }
      const trained = (trainFrames ?? [])
        .map(extractSeries)
        .filter((p): p is NonNullable<typeof p> => p != null);
      const level = forecastLevel(options);

      for (const series of data.series) {
        const points = extractSeries(series);
        if (!points) {
          continue;
        }
        history.push(toFrame(points.name, points.times, points.values));
        const fit = trained.length ? pickTrainingPoints(points, trained) : points;
        try {
          const resp = await getBackendSrv().post<ForecastResponse>(FORECAST_RESOURCE, {
            times: fit.times,
            values: fit.values,
            model: options.model,
            horizon: options.horizon,
            alpha: options.alpha,
            beta: options.beta,
            period: options.period,
            season: options.season,
            calendar: options.calendar,
            level,
          });
          forecasts.push(
            toForecastFrame(
              `${points.name} (forecast)`,
              resp.times,
              resp.values.map(nullToNaN),
              resp.lower,
              resp.upper,
              theme.colors.warning.main
            )
          );
        } catch (e) {
          if (!cancelled) {
            setError(e instanceof Error ? e.message : String(e));
          }
        }
      }

      if (!cancelled) {
        setFrames([...history, ...forecasts]);
      }
    }

    load();
    return () => {
      cancelled = true;
    };
  }, [
    data.series,
    data.request,
    timeRange.to,
    timeZone,
    options.model,
    options.horizon,
    options.alpha,
    options.beta,
    options.period,
    options.season,
    options.calendar,
    options.showInterval,
    options.interval,
    options.lookback,
    options.trainRange?.from,
    options.trainRange?.to,
    theme.colors.warning.main,
  ]);

  const plotFrames = useMemo(
    () =>
      applyFieldOverrides({
        data: frames,
        fieldConfig,
        replaceVariables,
        theme,
        timeZone,
      }),
    [frames, fieldConfig, replaceVariables, theme, timeZone]
  );

  if (data.series.length === 0) {
    return <PanelDataErrorView fieldConfig={fieldConfig} panelId={id} data={data} needsTimeField needsNumberField />;
  }

  let toMs = timeRange.to.valueOf();
  for (const frame of plotFrames) {
    const timeField = frame.fields.find((f) => f.type === FieldType.time);
    if (!timeField || timeField.values.length === 0) {
      continue;
    }
    const last = Number(timeField.values[timeField.values.length - 1]);
    if (Number.isFinite(last) && last > toMs) {
      toMs = last;
    }
  }

  return (
    <div style={{ width, height, position: 'relative' }}>
      {error && (
        <Alert title="Forecast failed" severity="error">
          {error}
        </Alert>
      )}
      <TimeSeries
        width={width}
        height={error ? height - 64 : height}
        timeRange={{ ...timeRange, to: dateTime(toMs) }}
        timeZone={timeZone}
        frames={plotFrames}
        legend={{ showLegend: true, displayMode: LegendDisplayMode.List, placement: 'bottom', calcs: [] }}
      >
        {(config, alignedFrame) => (
          <TooltipPlugin
            config={config}
            data={alignedFrame}
            timeZone={timeZone}
            mode={TooltipDisplayMode.Multi}
          />
        )}
      </TimeSeries>
    </div>
  );
};

function nullToNaN(v: number | null): number {
  return v == null ? NaN : v;
}

function toNullable(v: number | null | undefined): number | null {
  return v == null || Number.isNaN(Number(v)) ? null : Number(v);
}

function toForecastFrame(
  name: string,
  times: number[],
  values: number[],
  lower: Array<number | null> | undefined,
  upper: Array<number | null> | undefined,
  color?: string
): DataFrame {
  const frame = toFrame(name, times, values, color);
  const lo = lower?.map(toNullable);
  const hi = upper?.map(toNullable);
  if (!lo?.length || !hi?.length || !lo.some((v, i) => v != null && hi[i] != null)) {
    return frame;
  }
  const loName = `${name} lower`;
  const hiName = `${name} upper`;
  frame.addField({
    name: loName,
    type: FieldType.number,
    values: lo,
    config: {
      displayName: loName,
      custom: {
        lineWidth: 0,
        fillOpacity: 0,
        hideFrom: { legend: true, tooltip: true, viz: false },
      },
    },
  });
  frame.addField({
    name: hiName,
    type: FieldType.number,
    values: hi,
    config: {
      displayName: hiName,
      custom: {
        lineWidth: 0,
        fillOpacity: 20,
        fillBelowTo: loName,
        hideFrom: { legend: true, tooltip: true, viz: false },
      },
    },
  });
  return frame;
}

function toFrame(name: string, times: number[], values: Array<number | null | number>, color?: string): MutableDataFrame {
  const frame = new MutableDataFrame();
  frame.refId = name;
  frame.addField({ name: 'Time', type: FieldType.time, values: times });
  frame.addField({
    name,
    type: FieldType.number,
    values,
    config: {
      displayName: name,
      color: color ? { mode: 'fixed', fixedColor: color } : undefined,
    },
  });
  return frame;
}
