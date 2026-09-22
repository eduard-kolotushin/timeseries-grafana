import React, { useEffect, useMemo, useRef, useState } from 'react';
import {
  applyFieldOverrides,
  DataFrame,
  dateTime,
  FieldType,
  MutableDataFrame,
  PanelProps,
} from '@grafana/data';
import { locationService, PanelDataErrorView } from '@grafana/runtime';
import { LegendDisplayMode, TooltipDisplayMode } from '@grafana/schema';
import { Alert, Button, TimeSeries, TooltipPlugin, useTheme2 } from '@grafana/ui';
import { FORECAST_RESOURCE } from '../constants';
import { postResource } from './abortable';
import { dashboardUidFromPath } from './alertFromPanel';
import { cacheKey, fitOptions } from './cacheKey';
import { extractSeries, historyFrame } from './extract';
import {
  dashboardNowMs,
  forecastLevel,
  isInvalidForecastWindow,
  isInvalidTrainWindow,
  resolveForecastWindow,
  resolveTrainWindow,
  trainStepMs,
} from './lookback';
import { OverlayLoadGate } from './overlayInflight';
import { loadOverlayForecasts } from './overlayLoad';
import { drawableFrames, framesQueryKey, LoadedFrames, metricTargets, splitPanelFrames } from './mixed';
import { REASON_INVALID_RANGE, REASON_INVALID_TRAIN_RANGE } from './reasons';
import { clearRetrain, queueRetrain, retrainKey, takeRetrain } from './retrain';
import { refType, queryTrainingFrames, trainRejectReason } from './trainQuery';
import { summarizeTrainTargets } from './trainRewrite';
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
  title,
}) => {
  const theme = useTheme2();
  const allTargets = data.request?.targets;
  // Metric frames only; Mixed Forecast datasource frames are neither fitted nor plotted.
  const historyFrames = useMemo(
    () => splitPanelFrames(data.series, allTargets ?? []).history,
    [data.series, allTargets]
  );
  // Frames the panel can actually draw. A frame with no rows — an instant query evaluated
  // past its data — has nothing to plot and Grafana's TimeSeries panel throws on one, where
  // Grafana's own panel answers the same frame with `No data`; such a frame is never plotted.
  const graphableFrames = useMemo(
    () => historyFrames.filter((frame) => extractSeries(frame, historyFrames).length > 0),
    [historyFrames]
  );
  const [load, setLoad] = useState<LoadedFrames>();
  const [error, setError] = useState<string | null>(null);
  const [forecastToMs, setForecastToMs] = useState<number | undefined>();
  const [usedSaved, setUsedSaved] = useState(false);
  const [retrainNonce, setRetrainNonce] = useState(0);
  const loadGate = useRef(new OverlayLoadGate());
  const mounted = useRef(true);
  // The panel's own retrain slot: this dashboard's panel, not any panel with this id.
  const dashboardUid = data.request?.dashboardUID || dashboardUidFromPath(locationService.getLocation().pathname);
  const key = retrainKey(dashboardUid, id);
  const queryKey = framesQueryKey(data.request);

  useEffect(() => {
    mounted.current = true;
    const gate = loadGate.current;
    return () => {
      // On unmount nothing will take a queued retrain, and a stranded flag would fire on a
      // later remount of the same panel. This effect is declared before the load effect, so
      // on unmount it also blocks that effect's cleanup from re-queueing (and clears the
      // re-queue if it already ran).
      mounted.current = false;
      clearRetrain(key);
      gate.abortAll();
    };
  }, [key]);

  useEffect(() => {
    let cancelled = false;
    let finished = false;
    let retrain = false;
    const ac = loadGate.current.start(options.maxInflightLoads);

    async function load() {
      setError(null);
      const history: DataFrame[] = [];
      const nowMs = dashboardNowMs(timeZone);
      const window = resolveForecastWindow(options, nowMs, timeZone);

      const visible = graphableFrames.flatMap((series) => extractSeries(series, graphableFrames));
      for (const points of visible) {
        history.push(historyFrame(points));
      }

      if (isInvalidForecastWindow(window)) {
        if (!cancelled) {
          setError(REASON_INVALID_RANGE);
          setForecastToMs(undefined);
          setUsedSaved(false);
          setLoad({ key: queryKey, frames: history });
          finished = true;
        }
        return;
      }
      const trainWindow = resolveTrainWindow(options, timeRange.to.valueOf(), timeZone);
      const visibleFromMs = data.request?.range?.from?.valueOf();
      const visibleToMs = data.request?.range?.to?.valueOf();
      const targets = metricTargets(allTargets ?? []);
      if (isInvalidTrainWindow(trainWindow)) {
        if (!cancelled) {
          setError(REASON_INVALID_TRAIN_RANGE);
          setForecastToMs(undefined);
          setUsedSaved(false);
          setLoad({ key: queryKey, frames: history });
          finished = true;
        }
        return;
      }
      // Built once and shared: the probe needs it as much as the fit does, and it is what
      // tells the panel its targets can never train, even when it has nothing to draw.
      const rewriteWindow = {
        fromMs: trainWindow.fromMs,
        toMs: trainWindow.toMs,
        relative: trainWindow.relative,
        lookbackMs: trainWindow.lookbackMs,
        visibleFromMs,
        visibleToMs,
        intervalMs: trainStepMs(options.model, options.season, data.request?.intervalMs ?? 0),
      };
      const trainReject = trainRejectReason(data.request, rewriteWindow);
      if (trainReject) {
        if (!cancelled) {
          setError(trainReject);
          setForecastToMs(undefined);
          setUsedSaved(false);
          setLoad({ key: queryKey, frames: history });
          finished = true;
        }
        return;
      }
      if (!cancelled) {
        setForecastToMs(window.toMs);
      }

      retrain = takeRetrain(key);
      // Built once and shared: the probe needs it as much as the fit does. The summary
      // comes from the panel's own targets, so a probe can identify a row that has not
      // been retrained by a browser since the plugin started recording identity.
      const provenance = {
        panelId: id,
        panelTitle: title,
        dashboardUid,
        querySummary: summarizeTrainTargets(refType(targets[0]?.datasource), targets) || undefined,
      };
      const result = await loadOverlayForecasts({
        visible,
        fromMs: window.fromMs,
        toMs: window.toMs,
        level: forecastLevel(options),
        retrain,
        provenance,
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
          queryTrainingFrames(
            data.request,
            {
              ...rewriteWindow,
              // Identification for the schedule row this fit writes: which panel trains
              // on what. Never part of the cacheKey.
              provenance,
            },
            ac.signal
          ),
        signal: ac.signal,
        post: (body) => postResource<ForecastResponse>(FORECAST_RESOURCE, body, ac.signal),
      });
      if (cancelled || ac.signal.aborted) {
        return;
      }
      finished = true;
      setError(result.error);
      setUsedSaved(result.usedSaved);
      setLoad({
        key: queryKey,
        frames: [
          ...history,
          ...result.forecasts.map((fc) =>
            toForecastFrame(fc.name, fc.times, fc.values, fc.lower, fc.upper, theme.colors.warning.main)
          ),
        ],
      });
    }

    load().finally(() => loadGate.current.finish(ac));
    return () => {
      cancelled = true;
      // The click was not honored yet. A superseded load hands its retrain to the load
      // replacing it (this cleanup runs before that load's prologue, which takes the flag,
      // so the retrain happens exactly once); on unmount there is no successor and the
      // unmount effect above clears what this queues.
      if (retrain && !finished && mounted.current) {
        queueRetrain(key);
      }
    };
    // `options` is a new object whenever any panel option changes, so it covers every field the load reads.
  }, [id, key, queryKey, title, retrainNonce, data.request, graphableFrames, allTargets, timeRange.to, timeZone, options, theme.colors.warning.main, dashboardUid]);

  // The load's frames once they answer the panel's current query, the current history
  // before that: a superseded load never leaves the previous range's frames on screen.
  const plotFrames = useMemo(
    () =>
      applyFieldOverrides({
        data: drawableFrames(load, queryKey, graphableFrames),
        fieldConfig,
        replaceVariables,
        theme,
        timeZone,
      }),
    [load, queryKey, graphableFrames, fieldConfig, replaceVariables, theme, timeZone]
  );

  // Nothing to draw: Grafana's own empty state, with the reason the panel resolved above it.
  if (plotFrames.length === 0) {
    return (
      <>
        {error && (
          <Alert title="Forecast failed" severity="error">
            {error}
          </Alert>
        )}
        <PanelDataErrorView fieldConfig={fieldConfig} panelId={id} data={data} needsTimeField needsNumberField />
      </>
    );
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
        <Button
          size="sm"
          variant="secondary"
          fill="outline"
          type="button"
          onClick={() => {
            queueRetrain(key);
            setRetrainNonce((n) => n + 1);
          }}
        >
          Retrain
        </Button>
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
