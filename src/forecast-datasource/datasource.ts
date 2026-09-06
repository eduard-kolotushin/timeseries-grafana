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

  filterQuery(query: ForecastDataQuery): boolean {
    return Boolean(query.cacheKey) && !query.hide;
  }
}
