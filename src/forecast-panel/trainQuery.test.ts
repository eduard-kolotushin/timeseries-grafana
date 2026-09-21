import { DataFrame, DataQueryRequest, FieldType, MutableDataFrame, dateTime } from '@grafana/data';
import { of } from 'rxjs';
import { REASON_UNSUPPORTED_PROM_INSTANT } from './reasons';
import { queryTrainingFrames, trainRejectReason } from './trainQuery';

const mockGet = jest.fn();

jest.mock('@grafana/runtime', () => ({
  getDataSourceSrv: () => ({ get: mockGet }),
}));

const fromMs = Date.UTC(2026, 0, 1);
const toMs = fromMs + 7 * 86_400_000;

function frame(fieldName: string): MutableDataFrame {
  const f = new MutableDataFrame();
  f.addField({ name: 'Time', type: FieldType.time, values: [fromMs, toMs - 60_000] });
  f.addField({ name: fieldName, type: FieldType.number, values: [1, 2] });
  return f;
}

function request(refs: Array<{ uid: string; type: string }>): DataQueryRequest {
  return {
    targets: refs.map((ref, i) => ({
      refId: String.fromCharCode(65 + i),
      datasource: { uid: ref.uid, type: ref.type },
      expr: 'up',
    })),
    range: { from: dateTime(fromMs), to: dateTime(toMs), raw: { from: 'now-7d', to: 'now' } },
    scopedVars: {},
    intervalMs: 60_000,
    requestId: 'test',
  } as unknown as DataQueryRequest;
}

/** A datasource whose every group answers with `data`, recording the targets it was handed. */
function datasource(data: DataFrame[]) {
  const seen: unknown[][] = [];
  return {
    seen,
    ds: {
      type: 'prometheus',
      query: jest.fn((req: { targets: unknown[] }) => {
        seen.push(req.targets);
        return of({ data });
      }),
    },
  };
}

describe('trainRejectReason', () => {
  const rewriteWindow = { fromMs, toMs, intervalMs: 60_000 };

  const req = (targets: unknown[]): DataQueryRequest =>
    ({ targets, intervalMs: 60_000 } as unknown as DataQueryRequest);

  const prometheus = (extra: Record<string, unknown>) => ({
    refId: 'A',
    datasource: { uid: 'prometheus', type: 'prometheus' },
    expr: 'up',
    ...extra,
  });

  const druid = { refId: 'B', datasource: { uid: 'druid', type: 'grafadruid-druid-datasource' } };

  const cases: Array<[string, unknown[], string | undefined]> = [
    ['an instant Prometheus target', [prometheus({ instant: true, range: false })], REASON_UNSUPPORTED_PROM_INSTANT],
    ['a range Prometheus target', [prometheus({ instant: false, range: true })], undefined],
    [
      'an instant target beside a range target on the same datasource',
      [prometheus({ instant: true, range: false }), prometheus({ refId: 'B', instant: false, range: true })],
      undefined,
    ],
    ['a Druid target', [druid], undefined],
    [
      'an instant Prometheus target beside a trainable Druid target',
      [prometheus({ instant: true, range: false }), druid],
      undefined,
    ],
  ];

  it.each(cases)('%s → %s', (_name, targets, want) => {
    expect(trainRejectReason(req(targets), rewriteWindow)).toBe(want);
  });

  it('says nothing when the panel has no targets', () => {
    expect(trainRejectReason(undefined, rewriteWindow)).toBeUndefined();
    expect(trainRejectReason(req([]), rewriteWindow)).toBeUndefined();
  });
});

describe('queryTrainingFrames', () => {
  beforeEach(() => mockGet.mockReset());

  it('returns the exact query objects handed to the datasource plus the train window', async () => {
    const prom = datasource([frame('up')]);
    mockGet.mockResolvedValue(prom.ds);

    const result = await queryTrainingFrames(request([{ uid: 'prom', type: 'prometheus' }]), {
      fromMs,
      toMs,
      intervalMs: 60_000,
    });

    expect(result.frames).toHaveLength(1);
    expect(result.source).toEqual({
      datasourceUid: 'prom',
      queries: prom.seen[0],
      from: fromMs,
      to: toMs,
      relative: false,
      lookbackMs: 0,
      querySummary: 'PromQL: up',
    });
    // Identity, not a copy: the backend replays the untouched rewrite output.
    expect(result.source?.queries).toBe(prom.seen[0]);
  });

  it('carries a relative window into the source so a cron retrain can re-resolve it', async () => {
    const prom = datasource([frame('up')]);
    mockGet.mockResolvedValue(prom.ds);

    const result = await queryTrainingFrames(request([{ uid: 'prom', type: 'prometheus' }]), {
      fromMs,
      toMs,
      intervalMs: 60_000,
      relative: true,
      lookbackMs: toMs - fromMs,
    });

    expect(result.source).toEqual({
      datasourceUid: 'prom',
      queries: prom.seen[0],
      from: fromMs,
      to: toMs,
      relative: true,
      lookbackMs: toMs - fromMs,
      querySummary: 'PromQL: up',
    });
  });

  it('records the panel provenance that identifies the schedule row', async () => {
    const prom = datasource([frame('up')]);
    mockGet.mockResolvedValue(prom.ds);

    const result = await queryTrainingFrames(request([{ uid: 'prom', type: 'prometheus' }]), {
      fromMs,
      toMs,
      intervalMs: 60_000,
      provenance: { panelId: 7, panelTitle: 'CPU', dashboardUid: 'dash-1' },
    });

    expect(result.source?.panelId).toBe(7);
    expect(result.source?.panelTitle).toBe('CPU');
    expect(result.source?.dashboardUid).toBe('dash-1');
    expect(result.source?.querySummary).toBe('PromQL: up');
  });

  it('sends no provenance keys when the panel could not resolve them', async () => {
    const prom = datasource([frame('up')]);
    mockGet.mockResolvedValue(prom.ds);

    const result = await queryTrainingFrames(request([{ uid: 'prom', type: 'prometheus' }]), {
      fromMs,
      toMs,
      intervalMs: 60_000,
      provenance: { panelId: undefined, panelTitle: '', dashboardUid: undefined },
    });

    expect(result.source?.panelId).toBeUndefined();
    expect(result.source?.panelTitle).toBeUndefined();
    expect(result.source?.dashboardUid).toBeUndefined();
    expect(JSON.parse(JSON.stringify(result.source))).toEqual({
      datasourceUid: 'prom',
      queries: prom.seen[0],
      from: fromMs,
      to: toMs,
      relative: false,
      lookbackMs: 0,
      querySummary: 'PromQL: up',
    });
  });

  it('attributes the source to the first group that returned frames', async () => {
    const first = datasource([frame('up')]);
    const second = datasource([frame('other')]);
    mockGet.mockResolvedValueOnce(first.ds).mockResolvedValueOnce(second.ds);

    const result = await queryTrainingFrames(
      request([
        { uid: 'prom', type: 'prometheus' },
        { uid: 'other', type: 'prometheus' },
      ]),
      { fromMs, toMs, intervalMs: 60_000 }
    );

    expect(result.frames).toHaveLength(2);
    expect(result.source?.datasourceUid).toBe('prom');
    expect(result.source?.queries).toBe(first.seen[0]);
  });

  it('omits the source when no group returned frames', async () => {
    const empty = datasource([]);
    mockGet.mockResolvedValue(empty.ds);

    const result = await queryTrainingFrames(request([{ uid: 'prom', type: 'prometheus' }]), {
      fromMs,
      toMs,
      intervalMs: 60_000,
    });

    expect(result.frames).toBeNull();
    expect(result.source).toBeUndefined();
  });
});
