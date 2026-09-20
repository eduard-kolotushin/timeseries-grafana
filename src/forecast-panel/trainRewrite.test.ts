import { isoUtc, rfc3339Utc } from './lookback';
import {
  DS_DRUID,
  DS_OPENSEARCH,
  DS_POSTGRES,
  DS_POSTGRES_PLUGIN,
  DS_PROMETHEUS,
  rewritePostgresSql,
  rewriteTrainTargets,
  summarizeTrainTargets,
} from './trainRewrite';
import {
  REASON_UNSUPPORTED_OS_QUERY,
  REASON_UNSUPPORTED_PG_FORMAT,
  REASON_UNSUPPORTED_PROM_INSTANT,
} from './reasons';

const visFrom = Date.UTC(2026, 7, 20, 10, 0, 0);
const visTo = Date.UTC(2026, 7, 27, 16, 0, 0);
const trainFrom = visTo - 7 * 24 * 60 * 60 * 1000;
const trainTo = visTo;
const window = {
  fromMs: trainFrom,
  toMs: trainTo,
  visibleFromMs: visFrom,
  visibleToMs: visTo,
  intervalMs: 60_000,
};

describe('rewriteTrainTargets prometheus', () => {
  it('rejects instant queries', () => {
    const got = rewriteTrainTargets(
      DS_PROMETHEUS,
      [{ refId: 'A', expr: 'up', instant: true, range: false }],
      window
    );
    expect(got.targets).toEqual([]);
    expect(got.reason).toBe(REASON_UNSUPPORTED_PROM_INSTANT);
  });

  it('forces range, pins step to 1m, and leaves macros in expr', () => {
    const expr = 'rate(http_requests_total[$__rate_interval]) / $__interval';
    const got = rewriteTrainTargets(
      DS_PROMETHEUS,
      [
        {
          refId: 'A',
          expr,
          range: true,
          instant: false,
          exemplar: true,
          interval: '15s',
        },
      ],
      window
    );
    expect(got.reason).toBeUndefined();
    expect(got.targets).toEqual([
      {
        refId: 'A',
        expr,
        range: true,
        instant: false,
        exemplar: false,
        interval: '1m',
      },
    ]);
  });

  it('does not replace numbers in expr', () => {
    const expr = `up * ${visFrom}`;
    const [target] = rewriteTrainTargets(
      DS_PROMETHEUS,
      [{ refId: 'A', expr, range: true }],
      window
    ).targets;
    expect(target.expr).toBe(expr);
  });

  it('treats instant+range as a range query', () => {
    const [target] = rewriteTrainTargets(
      DS_PROMETHEUS,
      [{ refId: 'A', expr: 'up', instant: true, range: true }],
      window
    ).targets;
    expect(target).toMatchObject({ range: true, instant: false, exemplar: false, interval: '1m' });
  });
});

describe('rewriteTrainTargets opensearch', () => {
  it('pins date_histogram interval to the train step', () => {
    const got = rewriteTrainTargets(
      DS_OPENSEARCH,
      [
        {
          refId: 'A',
          queryType: 'lucene',
          luceneQueryType: 'Metric',
          query: '*',
          metrics: [{ id: '1', type: 'avg', field: 'value' }],
          bucketAggs: [
            { id: '2', type: 'date_histogram', field: '@timestamp', settings: { interval: 'auto', min_doc_count: '0' } },
          ],
        },
      ],
      window
    );
    expect(got.reason).toBeUndefined();
    expect(got.targets[0].bucketAggs[0].settings).toEqual({ interval: '1m', min_doc_count: '0' });
  });

  it('skips logs targets', () => {
    const got = rewriteTrainTargets(
      DS_OPENSEARCH,
      [{ refId: 'A', queryType: 'lucene', luceneQueryType: 'Logs', query: '*' }],
      window
    );
    expect(got.targets).toEqual([]);
    expect(got.reason).toBe(REASON_UNSUPPORTED_OS_QUERY);
  });

  it('keeps PPL time series queries', () => {
    const query = 'source = metrics | stats avg(value) by span(@timestamp, 1m)';
    const got = rewriteTrainTargets(
      DS_OPENSEARCH,
      [{ refId: 'A', queryType: 'PPL', format: 'time_series', query }],
      window
    );
    expect(got.reason).toBeUndefined();
    expect(got.targets[0].query).toBe(query);
  });
});

describe('rewriteTrainTargets postgres', () => {
  it('leaves leftover time macros unchanged', () => {
    const rawSql = 'SELECT ts AS time, value FROM samples WHERE $__timeFilter(ts) ORDER BY 1';
    const got = rewriteTrainTargets(
      DS_POSTGRES,
      [{ refId: 'A', format: 'time_series', rawSql }],
      window
    );
    expect(got.reason).toBeUndefined();
    expect(got.targets[0].rawSql).toBe(rawSql);
    expect(got.targets[0].format).toBe('time_series');
  });

  it('rewrites expanded BETWEEN ISO literals to the train window', () => {
    const rawSql = `SELECT ts AS time, value FROM samples WHERE ts BETWEEN '${isoUtc(visFrom)}' AND '${isoUtc(visTo)}' ORDER BY 1`;
    const got = rewriteTrainTargets(
      DS_POSTGRES_PLUGIN,
      [{ refId: 'A', format: 'time_series', rawSql }],
      window
    );
    expect(got.targets[0].rawSql).toContain(isoUtc(trainFrom));
    expect(got.targets[0].rawSql).toContain(isoUtc(trainTo));
    expect(got.targets[0].rawSql).not.toContain(isoUtc(visFrom));
  });

  it('rewrites RFC3339 SQL expansions without milliseconds', () => {
    const rawSql = `SELECT ts AS time, value FROM samples WHERE $__timeFilter(ts)`
      .replace('$__timeFilter(ts)', `ts BETWEEN '${rfc3339Utc(visFrom)}' AND '${rfc3339Utc(visTo)}'`);
    // leftover macros would skip rewrite; this string has none
    expect(rawSql).not.toContain('$__timeFilter');
    const got = rewritePostgresSql(rawSql, window);
    expect(got).toContain(rfc3339Utc(trainFrom));
    expect(got).toContain(rfc3339Utc(trainTo));
  });

  it('skips table and EXPLAIN queries', () => {
    expect(
      rewriteTrainTargets(DS_POSTGRES, [{ refId: 'A', format: 'table', rawSql: 'SELECT 1' }], window)
    ).toEqual({ targets: [], reason: REASON_UNSUPPORTED_PG_FORMAT });
    expect(
      rewriteTrainTargets(
        DS_POSTGRES,
        [{ refId: 'A', format: 'time_series', rawSql: 'EXPLAIN SELECT ts, value FROM samples' }],
        window
      ).reason
    ).toBe(REASON_UNSUPPORTED_PG_FORMAT);
  });

  it('does not replace arbitrary integers in SQL', () => {
    const rawSql = `SELECT ts AS time, value FROM samples WHERE id = ${visFrom} AND $__timeFilter(ts)`;
    const got = rewriteTrainTargets(DS_POSTGRES, [{ refId: 'A', format: 'time_series', rawSql }], window);
    expect(got.targets[0].rawSql).toContain(String(visFrom));
  });
});

describe('summarizeTrainTargets', () => {
  it('labels the rewrite output by datasource type', () => {
    for (const tc of [
      {
        name: 'prometheus',
        dsType: DS_PROMETHEUS,
        targets: [{ expr: 'sum(rate(http_requests_total[5m]))' }],
        want: 'PromQL: sum(rate(http_requests_total[5m]))',
      },
      {
        name: 'opensearch ppl',
        dsType: DS_OPENSEARCH,
        targets: [{ queryType: 'ppl', query: 'source=logs | stats avg(bytes) by span(time, 1m)' }],
        want: 'PPL: source=logs | stats avg(bytes) by span(time, 1m)',
      },
      {
        name: 'opensearch lucene',
        dsType: DS_OPENSEARCH,
        targets: [{ queryType: 'lucene', query: 'status:500' }],
        want: 'Lucene: status:500',
      },
      {
        name: 'postgres',
        dsType: DS_POSTGRES,
        targets: [{ rawSql: 'SELECT\n  ts,\n  value\n  FROM samples' }],
        want: 'SQL: SELECT ts, value FROM samples',
      },
      {
        name: 'postgres plugin alias',
        dsType: DS_POSTGRES_PLUGIN,
        targets: [{ rawSql: 'SELECT 1' }],
        want: 'SQL: SELECT 1',
      },
      {
        name: 'druid',
        dsType: DS_DRUID,
        targets: [{ builder: { query: 'SELECT __time, value FROM metrics' } }],
        want: 'Druid SQL: SELECT __time, value FROM metrics',
      },
      {
        // The Druid builder UI (what the sandbox dashboard uses) has no query text:
        // identify the table and rollup instead of leaving the row unidentified.
        name: 'druid builder',
        dsType: DS_DRUID,
        targets: [
          {
            builder: {
              dataSource: { name: 'minuteweek', type: 'table' },
              granularity: 'minute',
              aggregations: [{ fieldName: 'value', name: 'value', type: 'doubleSum' }],
            },
          },
        ],
        want: 'Druid: minuteweek · minute',
      },
      { name: 'druid builder without a table', dsType: DS_DRUID, targets: [{ builder: { granularity: 'minute' } }], want: '' },
      // No query text to show: the Source cell stays empty rather than inventing one.
      { name: 'testdata', dsType: 'grafana-testdata-datasource', targets: [{ scenarioId: 'random_walk' }], want: '' },
      { name: 'unknown type', dsType: '', targets: [{ something: 'else' }], want: '' },
      { name: 'no targets', dsType: DS_PROMETHEUS, targets: [], want: '' },
      { name: 'blank expr', dsType: DS_PROMETHEUS, targets: [{ expr: '   ' }], want: '' },
      {
        name: 'skips a target without text',
        dsType: DS_PROMETHEUS,
        targets: [{}, { expr: 'up' }],
        want: 'PromQL: up',
      },
    ]) {
      expect({ name: tc.name, got: summarizeTrainTargets(tc.dsType, tc.targets) }).toEqual({
        name: tc.name,
        got: tc.want,
      });
    }
  });

  it('caps a long query so the row keeps one line', () => {
    const summary = summarizeTrainTargets(DS_PROMETHEUS, [{ expr: 'x'.repeat(200) }]);
    expect(summary).toHaveLength(120);
    expect(summary.endsWith('…')).toBe(true);
    expect(summary.startsWith('PromQL: xxx')).toBe(true);
  });
});

describe('rewriteTrainTargets other', () => {
  it('keeps the Druid interval rewrite', () => {
    const got = rewriteTrainTargets(
      'grafadruid-druid-datasource',
      [
        {
          builder: {
            intervals: { type: 'intervals', intervals: ['${__from:date:iso}/${__to:date:iso}'] },
          },
        },
      ],
      window
    );
    expect(got.targets[0].builder.intervals.intervals).toEqual([`${isoUtc(trainFrom)}/${isoUtc(trainTo)}`]);
  });
});
