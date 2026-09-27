import React from 'react';
import { act, render, screen, waitFor } from '@testing-library/react';
import {
  DataFrame,
  dateTime,
  DataQueryRequest,
  EventBus,
  FieldConfigSource,
  FieldType,
  LoadingState,
  PanelData,
  PanelProps,
  TimeRange,
  toDataFrame,
} from '@grafana/data';
import { ForecastOptions, ForecastResponse } from './types';

const mockPost = jest.fn();
const mockDrawn: DataFrame[][] = [];

jest.mock('./abortable', () => ({ postResource: (...args: unknown[]) => mockPost(...args) }));

jest.mock('@grafana/runtime', () => ({
  locationService: { getLocation: () => ({ pathname: '/d/dash-a/view' }) },
  PanelDataErrorView: () => null,
  getBackendSrv: () => ({ fetch: jest.fn() }),
}));

jest.mock('./trainQuery', () => ({
  ...jest.requireActual('./trainQuery'),
  refType: () => 'prom',
  queryTrainingFrames: () => Promise.resolve({ frames: null, reason: 'no training frames in this test' }),
  trainRejectReason: () => undefined,
}));

jest.mock('@grafana/ui', () => ({
  useTheme2: () => require('@grafana/data').createTheme(),
  Alert: () => null,
  Button: ({ children }: { children?: React.ReactNode }) =>
    require('react').createElement('button', { type: 'button' }, children),
  TooltipPlugin: () => null,
  TimeSeries: ({ frames }: { frames: DataFrame[] }) => {
    mockDrawn.push(frames);
    return null;
  },
}));

import { ForecastPanel } from './ForecastPanel';

const options: ForecastOptions = {
  model: 'holt',
  alpha: 0.8,
  beta: 0.2,
  period: 1,
  season: 'hour',
  calendar: '',
  showInterval: false,
  interval: 0.95,
  trainRange: { from: '', to: '' },
  forecastRange: { from: '', to: '' },
  maxInflightLoads: 1,
};

const fieldConfig: FieldConfigSource = { defaults: {}, overrides: [] };

/** The panel never touches the event bus; the fixture only has to satisfy the prop type. */
const eventBus = {} as EventBus;

const rangeA: TimeRange = {
  from: dateTime(Date.UTC(2026, 8, 21, 0, 0, 0)),
  to: dateTime(Date.UTC(2026, 8, 21, 6, 0, 0)),
  raw: { from: 'now-6h', to: 'now' },
};
const rangeB: TimeRange = {
  from: dateTime(Date.UTC(2026, 8, 20, 0, 0, 0)),
  to: dateTime(Date.UTC(2026, 8, 20, 6, 0, 0)),
  raw: { from: 'now-30h', to: 'now-24h' },
};

/** A history frame carrying what a datasource supplies: a unit on the field and a frame meta. */
function historyFrame(range: TimeRange, name: string): DataFrame {
  const frame = toDataFrame({
    refId: 'A',
    fields: [
      { name: 'Time', type: FieldType.time, values: [range.from.valueOf(), range.to.valueOf()] },
      { name, type: FieldType.number, values: [1, 2], config: { unit: 'bytes' } },
    ],
  });
  frame.meta = { executedQueryString: `SELECT 1 -- ${name}` };
  return frame;
}

function props(
  range: TimeRange,
  series: DataFrame[],
  overrides: Partial<ForecastOptions> = {}
): PanelProps<ForecastOptions> {
  const request: DataQueryRequest = {
    requestId: 'r1',
    interval: '1m',
    intervalMs: 60_000,
    range,
    scopedVars: {},
    targets: [{ refId: 'A', datasource: { uid: 'prom', type: 'prometheus' } }],
    timezone: 'utc',
    app: 'dashboard',
    startTime: 0,
    dashboardUID: 'dash-a',
  };
  const data: PanelData = { state: LoadingState.Done, series, timeRange: range, request };
  return {
    id: 7,
    data,
    timeRange: range,
    timeZone: 'utc',
    options: { ...options, ...overrides },
    transparent: false,
    width: 400,
    height: 300,
    fieldConfig,
    renderCounter: 0,
    title: 'CPU',
    eventBus,
    onOptionsChange: () => {},
    onFieldConfigChange: () => {},
    replaceVariables: (value: string) => value,
    onChangeTimeRange: () => {},
  };
}

let settle: ((response: ForecastResponse) => void) | undefined;

function pendingPost(): Promise<ForecastResponse> {
  return new Promise<ForecastResponse>((resolve) => {
    settle = resolve;
  });
}

/** The frames the panel last handed to the visualization. */
function drawn(): DataFrame[] {
  return mockDrawn[mockDrawn.length - 1] ?? [];
}

function drawnFieldNames(): string[] {
  return drawn().map((frame) => frame.fields.find((field) => field.type === FieldType.number)?.name ?? '');
}

describe('ForecastPanel frames', () => {
  beforeEach(() => {
    mockPost.mockReset();
    mockDrawn.length = 0;
    settle = undefined;
  });

  it('never draws the previous range once the panel query changed', async () => {
    mockPost.mockReturnValueOnce(pendingPost());
    const view = render(<ForecastPanel {...props(rangeA, [historyFrame(rangeA, 'cpu-a')])} />);
    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(1));
    settle?.({ times: [rangeA.to.valueOf() + 60_000], values: [42] });
    await waitFor(() => expect(drawnFieldNames()).toEqual(['cpu-a', 'cpu-a (forecast)']));

    // The new range's load stays in flight while the query change lands.
    mockPost.mockReturnValueOnce(pendingPost());
    view.rerender(<ForecastPanel {...props(rangeB, [historyFrame(rangeB, 'cpu-b')])} />);

    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(2));
    expect(drawnFieldNames()).toEqual(['cpu-b']);
    expect(drawn()[0].meta?.executedQueryString).toBe('SELECT 1 -- cpu-b');

    settle?.({ times: [rangeB.to.valueOf() + 60_000], values: [7] });
    await waitFor(() => expect(drawnFieldNames()).toEqual(['cpu-b', 'cpu-b (forecast)']));
    // The rebuilt history keeps what the datasource put on the field and on the frame.
    expect(drawn()[0].fields[1].config.unit).toBe('bytes');
    expect(drawn()[0].meta?.executedQueryString).toBe('SELECT 1 -- cpu-b');
  });

  it('keeps drawing the completed load while nothing about the query changed', async () => {
    mockPost.mockReturnValueOnce(pendingPost());
    const first = <ForecastPanel {...props(rangeA, [historyFrame(rangeA, 'cpu-a')])} />;
    const view = render(first);
    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(1));
    settle?.({ times: [rangeA.to.valueOf() + 60_000], values: [42] });
    await waitFor(() => expect(drawnFieldNames()).toEqual(['cpu-a', 'cpu-a (forecast)']));

    // A re-render with the same query: the forecast must not disappear while a new load runs.
    mockPost.mockReturnValueOnce(pendingPost());
    await act(async () => {
      view.rerender(<ForecastPanel {...props(rangeA, [historyFrame(rangeA, 'cpu-a')])} />);
    });
    expect(drawnFieldNames()).toEqual(['cpu-a', 'cpu-a (forecast)']);
  });

  it('links the interval bands under the forecast and hides them from the legend', async () => {
    mockPost.mockReturnValueOnce(pendingPost());
    render(<ForecastPanel {...props(rangeA, [historyFrame(rangeA, 'cpu-a')], { showInterval: true })} />);
    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(1));
    settle?.({ times: [rangeA.to.valueOf() + 60_000], values: [42], lower: [40], upper: [44] });
    await waitFor(() => expect(drawn()).toHaveLength(2));

    const forecast = drawn()[1];
    expect(forecast.fields.map((field) => field.name)).toEqual([
      'Time',
      'cpu-a (forecast)',
      'cpu-a (forecast) lower',
      'cpu-a (forecast) upper',
    ]);
    const [lo, hi] = forecast.fields.slice(2).map((field) => field.config.custom!);
    // The upper band fills down to the lower one, and neither is a line of its own:
    // they describe the forecast's uncertainty, so they stay out of legend and tooltip.
    expect(hi.fillBelowTo).toBe('cpu-a (forecast) lower');
    expect(hi.fillOpacity).toBe(20);
    expect(lo.fillOpacity).toBe(0);
    expect(lo.hideFrom).toEqual({ legend: true, tooltip: true, viz: false });
    expect(hi.hideFrom).toEqual({ legend: true, tooltip: true, viz: false });
  });

  it('draws the forecast alone when the reply carries no interval', async () => {
    mockPost.mockReturnValueOnce(pendingPost());
    render(<ForecastPanel {...props(rangeA, [historyFrame(rangeA, 'cpu-a')])} />);
    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(1));
    settle?.({ times: [rangeA.to.valueOf() + 60_000], values: [42] });
    await waitFor(() => expect(drawnFieldNames()).toEqual(['cpu-a', 'cpu-a (forecast)']));
    expect(drawn()[1].fields).toHaveLength(2);
  });

  it('offers Retrain when there is nothing to draw', async () => {
    render(<ForecastPanel {...props(rangeA, [])} />);
    // The empty state is Grafana's own view plus the reason; the action must still be there.
    expect(await screen.findByRole('button', { name: 'Retrain' })).toBeInTheDocument();
    expect(mockPost).not.toHaveBeenCalled();
  });
});
