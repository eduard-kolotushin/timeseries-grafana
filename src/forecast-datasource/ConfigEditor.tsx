import React from 'react';
import { DataSourcePluginOptionsEditorProps } from '@grafana/data';
import { Alert, Field, FieldSet, Input } from '@grafana/ui';
import { ForecastDataSourceOptions, ForecastSecureJsonData } from './types';

type Props = DataSourcePluginOptionsEditorProps<ForecastDataSourceOptions, ForecastSecureJsonData>;

/**
 * The snapshot store is a deployment parameter, so this editor has no store fields: a save here can never
 * overwrite a provisioned value. The backend resolves the DSN from env `FORECAST_STORE_*`,
 * `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_DATASOURCE_*`, the merged `[plugin.eduardkolotushin-forecast-datasource]`
 * ini section, and finally this datasource's provisioned jsonData/secureJsonData — in that order, per field.
 * The read-only line below is a hint at what provisioning gave this instance, not the effective DSN.
 */
export function ConfigEditor({ options }: Props) {
  const json = options.jsonData ?? {};
  const provisioned = Object.keys(json).filter((key) => key.toLowerCase().startsWith('store'));

  return (
    <div>
      <Alert title="Fitted snapshots" severity="info">
        This datasource Restores a snapshot after you train on a Forecast overlay panel. Its own process needs the
        store itself: Grafana 12.4+ does not forward host <code>FORECAST_STORE_*</code> into plugin processes, and
        alerting <code>QueryData</code> does not receive the app&apos;s jsonData. Configure the store by deployment —
        env, <code>GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_DATASOURCE_*</code>, the{' '}
        <code>[plugin.eduardkolotushin-forecast-datasource]</code> section of{' '}
        <code>conf/forecast.ini.template</code>, or provisioned jsonData (<code>storeUrl</code> /{' '}
        <code>storeHost</code>, <code>storePort</code>, <code>storeDatabase</code>, <code>storeUser</code>,{' '}
        <code>storeSslMode</code>, <code>storePassword</code>) — not here.
      </Alert>
      <FieldSet label="Snapshot store">
        <Field
          label="Store keys in this datasource's jsonData"
          description="Provisioned values are listed for reference; the backend also resolves env and the ini section, which are not shown here. No keys and no env means persist off: every query answers needTrain."
        >
          <Input readOnly value={provisioned.length ? provisioned.join(', ') : 'none'} />
        </Field>
      </FieldSet>
    </div>
  );
}
