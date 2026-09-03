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
import { cacheKey, fitOptions } from './cacheKey';
import { extractSeries } from './extract';
import {
  dashboardNowMs,
  forecastLevel,
  isInvalidForecastWindow,
  resolveForecastWindow,
  resolveTrainWindow,
  trainStepMs,
} from './lookback';
import { loadOverlayForecasts } from './overlayLoad';
import { metricTargets, splitPanelFrames } from './mixed';
import { REASON_INVALID_RANGE } from './reasons';
import { queueRetrain, takeRetrain } from './retrain';
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
  const [frames, setFrames] = useState<DataFrame[]>(() =>
    splitPanelFrames(data.series, data.request?.targets ?? []).history
  );
  const [error, setError] = useState<string | null>(null);
  const [forecastToMs, setForecastToMs] = useState<number | undefined>();
  const [usedSaved, setUsedSaved] = useState(false);
  const [retrainNonce, setRetrainNonce] = useState(0);

  useEffect(() => {
    let cancelled = false;
    let finished = false;
    let retrain = false;

    async function load() {
      setError(null);
      const history: DataFrame[] = [];
      const nowMs = dashboardNowMs(timeZone);
      const window = resolveForecastWindow(options, nowMs, timeZone);

      const allTargets = data.request?.targets ?? [];
      const { history: historyFrames } = splitPanelFrames(data.series, allTargets);
      const visible = historyFrames.flatMap((series) => extractSeries(series, historyFrames));
      for (const points of visible) {
        history.push(toFrame(points.name, points.times, points.values));
      }

      if (isInvalidForecastWindow(window)) {
        if (!cancelled) {
          setError(REASON_INVALID_RANGE);
          setForecastToMs(undefined);
          setUsedSaved(false);
          setFrames(history);
          finished = true;
        }
        return;
      }
      if (!cancelled) {
        setForecastToMs(window.toMs);
      }

      retrain = takeRetrain(id);
      const { fromMs: trainFromMs, toMs: trainToMs } = resolveTrainWindow(
        options,
        timeRange.to.valueOf(),
        timeZone
      );
      const visibleFromMs = data.request?.range?.from?.valueOf();
      const visibleToMs = data.request?.range?.to?.valueOf();
      const targets = metricTargets(allTargets);
      const result = await loadOverlayForecasts({
        visible,
        fromMs: window.fromMs,
        toMs: window.toMs,
        level: forecastLevel(options),
        retrain,
        fitBody: fitOptions(options),
        cacheKeyFor: (seriesName) =>
          cacheKey({
            targets,
            visibleFromMs,
            visibleToMs,
            options,
            seriesName,
          }),
        queryTrain: () =>
          queryTrainingFrames(data.request, {
            fromMs: trainFromMs,
            toMs: trainToMs,
            visibleFromMs,
            visibleToMs,
            intervalMs: trainStepMs(options.model, options.season, data.request?.intervalMs ?? 0),
          }),
        post: (body) => getBackendSrv().post<ForecastResponse>(FORECAST_RESOURCE, body),
      });
      if (cancelled) {
        return;
      }
      finished = true;
      setError(result.error);
      setUsedSaved(result.usedSaved);
      setFrames([
        ...history,
        ...result.forecasts.map((fc) =>
          toForecastFrame(fc.name, fc.times, fc.values, fc.lower, fc.upper, theme.colors.warning.main)
        ),
      ]);
    }

    load();
    return () => {
      cancelled = true;
      if (retrain && !finished) {
        queueRetrain(id);
      }
    };
  }, [
    id,
    retrainNonce,
    data.series,
    data.request,
    timeRange.to,
    timeZone,
    options,
    options.model,
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
    options.forecastRange?.from,
    options.forecastRange?.to,
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

  const { history: historyFrames } = splitPanelFrames(data.series, data.request?.targets ?? []);
  if (historyFrames.length === 0 && data.series.length === 0) {
    return <PanelDataErrorView fieldConfig={fieldConfig} panelId={id} data={data} needsTimeField needsNumberField />;
  }

  let toMs = timeRange.to.valueOf();
  if (forecastToMs != null && forecastToMs > toMs) {
    toMs = forecastToMs;
  }
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
      <div
        style={{
          position: 'absolute',
          top: 4,
          right: 8,
          zIndex: 1,
          display: 'flex',
          gap: 8,
          alignItems: 'center',
        }}
      >
        {usedSaved && !error && (
          <span style={{ fontSize: 12, opacity: 0.8 }}>Using saved model</span>
        )}
        <button
          type="button"
          onClick={() => {
            queueRetrain(id);
            setRetrainNonce((n) => n + 1);
          }}
          style={{ fontSize: 12 }}
        >
          Retrain
        </button>
      </div>
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
