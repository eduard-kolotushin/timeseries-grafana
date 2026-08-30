import { DataQueryRequest, DataQueryResponse, DataSourceInstanceSettings } from '@grafana/data';
import { DataSourceWithBackend } from '@grafana/runtime';
import { Observable } from 'rxjs';
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

  query(request: DataQueryRequest<ForecastDataQuery>): Observable<DataQueryResponse> {
    return super.query(request);
  }
}
