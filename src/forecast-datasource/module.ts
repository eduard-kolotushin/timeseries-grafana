import { DataSourcePlugin } from '@grafana/data';
import { ConfigEditor } from './ConfigEditor';
import { ForecastDataSource } from './datasource';
import { QueryEditor } from './QueryEditor';
import { ForecastDataQuery, ForecastDataSourceOptions, ForecastSecureJsonData } from './types';

export const plugin = new DataSourcePlugin<
  ForecastDataSource,
  ForecastDataQuery,
  ForecastDataSourceOptions,
  ForecastSecureJsonData
>(ForecastDataSource)
  .setConfigEditor(ConfigEditor)
  .setQueryEditor(QueryEditor);
