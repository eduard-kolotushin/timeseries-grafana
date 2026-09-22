import { DataSourceInstanceSettings } from '@grafana/data';
import { ForecastDataSource } from './datasource';
import { ForecastDataQuery, FORECAST_DATASOURCE_TYPE } from './types';

const settings = {
  uid: 'fc',
  name: 'Forecast',
  type: FORECAST_DATASOURCE_TYPE,
  meta: {},
  jsonData: {},
  access: 'proxy',
  readOnly: false,
} as unknown as DataSourceInstanceSettings;

const datasource = new ForecastDataSource(settings);

function query(partial: Partial<ForecastDataQuery>): ForecastDataQuery {
  return { refId: 'A', ...partial } as ForecastDataQuery;
}

describe('filterQuery', () => {
  const cases: Array<[string, Partial<ForecastDataQuery>, boolean]> = [
    // The editor computes `cacheKey` asynchronously, so a row that has none yet is still a
    // real query: the backend answers it with `needTrain` instead of the panel showing `No data`.
    ['a row whose cacheKey has not landed yet', {}, true],
    ['a keyed row', { cacheKey: 'ab'.repeat(32) }, true],
    ['a hidden row', { cacheKey: 'ab'.repeat(32), hide: true }, false],
  ];

  it.each(cases)('%s → sent=%s', (_name, partial, sent) => {
    expect(datasource.filterQuery(query(partial))).toBe(sent);
  });
});
