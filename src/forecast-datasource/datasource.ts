import { DataSourceInstanceSettings } from '@grafana/data';
import { DataSourceWithBackend } from '@grafana/runtime';
import { defaultForecastQuery, ForecastDataQuery, ForecastDataSourceOptions } from './types';

export class ForecastDataSource extends DataSourceWithBackend<ForecastDataQuery, ForecastDataSourceOptions> {
  constructor(instanceSettings: DataSourceInstanceSettings<ForecastDataSourceOptions>) {
    super(instanceSettings);
  }

  getDefaultQuery(): Partial<ForecastDataQuery> {
    return { ...defaultForecastQuery };
  }

  /**
   * Every visible row is sent. A row without a cache key is a real query: the backend
   * answers it with the `needTrain` reason instead of the panel showing `No data`.
   */
  filterQuery(query: ForecastDataQuery): boolean {
    return !query.hide;
  }
}
