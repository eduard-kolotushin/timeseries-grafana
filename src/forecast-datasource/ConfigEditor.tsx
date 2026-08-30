import React, { ChangeEvent } from 'react';
import { DataSourcePluginOptionsEditorProps } from '@grafana/data';
import { Alert, Field, FieldSet, Input, SecretInput } from '@grafana/ui';
import { ForecastDataSourceOptions, ForecastSecureJsonData } from './types';

type Props = DataSourcePluginOptionsEditorProps<ForecastDataSourceOptions, ForecastSecureJsonData>;

export function ConfigEditor({ options, onOptionsChange }: Props) {
  const json = options.jsonData ?? {};
  const secureJsonData = options.secureJsonData ?? {};
  const passwordConfigured = Boolean(options.secureJsonFields?.storePassword);

  const setJson = (patch: Partial<ForecastDataSourceOptions>) => {
    onOptionsChange({ ...options, jsonData: { ...json, ...patch } });
  };

  return (
    <div>
      <Alert title="Fitted snapshots" severity="info">
        This datasource Restores a snapshot after you train on a Forecast overlay panel. Grafana 12.4+ does not
        forward host <code>FORECAST_STORE_*</code> into plugin processes, and alerting QueryData does not receive the
        app Configuration jsonData. Set the store here, provision the same keys, or merge{' '}
        <code>[plugin.eduardkolotushin-forecast-datasource]</code> from <code>conf/forecast.ini.template</code>.
      </Alert>
      <FieldSet label="Snapshot store">
        <Field label="Host">
          <Input
            value={json.storeHost ?? ''}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setJson({ storeHost: e.currentTarget.value })}
          />
        </Field>
        <Field label="Port">
          <Input
            value={String(json.storePort ?? '5432')}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setJson({ storePort: e.currentTarget.value })}
          />
        </Field>
        <Field label="Database">
          <Input
            value={json.storeDatabase ?? 'overlay'}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setJson({ storeDatabase: e.currentTarget.value })}
          />
        </Field>
        <Field label="User">
          <Input
            value={json.storeUser ?? 'overlay'}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setJson({ storeUser: e.currentTarget.value })}
          />
        </Field>
        <Field label="SSL mode">
          <Input
            value={json.storeSslMode ?? 'disable'}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setJson({ storeSslMode: e.currentTarget.value })}
          />
        </Field>
        <Field label="Password">
          <SecretInput
            isConfigured={passwordConfigured}
            value={secureJsonData.storePassword ?? ''}
            onChange={(e: ChangeEvent<HTMLInputElement>) =>
              onOptionsChange({
                ...options,
                secureJsonData: { ...secureJsonData, storePassword: e.currentTarget.value },
              })
            }
            onReset={() =>
              onOptionsChange({
                ...options,
                secureJsonFields: { ...options.secureJsonFields, storePassword: false },
                secureJsonData: { ...secureJsonData, storePassword: '' },
              })
            }
          />
        </Field>
      </FieldSet>
    </div>
  );
}
