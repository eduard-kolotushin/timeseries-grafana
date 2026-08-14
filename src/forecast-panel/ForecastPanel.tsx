import React, { useEffect, useState } from 'react';
import { DataFrame, dateTime, FieldType, MutableDataFrame, PanelProps } from '@grafana/data';
import { getBackendSrv, PanelDataErrorView } from '@grafana/runtime';
import { LegendDisplayMode } from '@grafana/schema';
import { Alert, TimeSeries, useTheme2 } from '@grafana/ui';
import { FORECAST_RESOURCE } from '../constants';
import { extractSeries } from './extract';
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

      for (const series of data.series) {
        const points = extractSeries(series);
        if (!points) {
          continue;
        }
        history.push(toFrame(points.name, points.times, points.values));
        try {
          const resp = await getBackendSrv().post<ForecastResponse>(FORECAST_RESOURCE, {
            times: points.times,
            values: points.values,
            model: options.model,
            horizon: options.horizon,
            alpha: options.alpha,
            beta: options.beta,
            period: options.period,
          });
          forecasts.push(
            toFrame(`${points.name} (forecast)`, resp.times, resp.values.map(nullToNaN), theme.colors.warning.main)
          );
        } catch (e) {
          if (!cancelled) {
            setError(e instanceof Error ? e.message : String(e));
          }
          return;
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
    options.model,
    options.horizon,
    options.alpha,
    options.beta,
    options.period,
    theme.colors.warning.main,
  ]);

  if (data.series.length === 0) {
    return <PanelDataErrorView fieldConfig={fieldConfig} panelId={id} data={data} needsTimeField needsNumberField />;
  }

  let toMs = timeRange.to.valueOf();
  for (const frame of frames) {
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
        frames={frames}
        legend={{ showLegend: true, displayMode: LegendDisplayMode.List, placement: 'bottom', calcs: [] }}
      />
    </div>
  );
};

function nullToNaN(v: number | null): number {
  return v == null ? NaN : v;
}

function toFrame(name: string, times: number[], values: Array<number | null | number>, color?: string): DataFrame {
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
