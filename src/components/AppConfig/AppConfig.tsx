import React, { ChangeEvent, useState } from 'react';
import { AppPluginMeta, PluginConfigPageProps } from '@grafana/data';
import { getBackendSrv } from '@grafana/runtime';
import { Button, Field, FieldSet, Input, SecretInput } from '@grafana/ui';
import { testIds } from '../testIds';

export type ForecastStoreJsonData = {
  storeHost?: string;
  storePort?: number | string;
  storeDatabase?: string;
  storeUser?: string;
  storeSslMode?: string;
};

export type AppConfigProps = PluginConfigPageProps<AppPluginMeta<ForecastStoreJsonData>>;

const AppConfig = ({ plugin }: AppConfigProps) => {
  const json = plugin.meta.jsonData ?? {};
  const [storeHost, setStoreHost] = useState(json.storeHost ?? '');
  const [storePort, setStorePort] = useState(String(json.storePort ?? '5432'));
  const [storeDatabase, setStoreDatabase] = useState(json.storeDatabase ?? 'overlay');
  const [storeUser, setStoreUser] = useState(json.storeUser ?? 'overlay');
  const [storeSslMode, setStoreSslMode] = useState(json.storeSslMode ?? 'disable');
  const [storePassword, setStorePassword] = useState('');
  const [passwordConfigured, setPasswordConfigured] = useState(Boolean(plugin.meta.secureJsonFields?.storePassword));
  const [saving, setSaving] = useState(false);

  const save = async () => {
    setSaving(true);
    try {
      await getBackendSrv().post(`/api/plugins/${plugin.meta.id}/settings`, {
        enabled: true,
        pinned: plugin.meta.pinned,
        jsonData: {
          storeHost,
          storePort: Number(storePort) || 5432,
          storeDatabase,
          storeUser,
          storeSslMode,
        },
        secureJsonData: passwordConfigured && !storePassword ? {} : { storePassword },
      });
    } finally {
      setSaving(false);
    }
  };

  return (
    <div data-testid={testIds.appConfig.submit}>
      <p>
        Enable this app, then use the <strong>Forecast overlay</strong> panel on a dashboard. Fitted models are stored
        in Postgres (schema <code>forecast</code>), not the Grafana SQL datasource and not Druid metadata.
      </p>
      <FieldSet label="Snapshot store">
        <Field label="Host">
          <Input value={storeHost} onChange={(e: ChangeEvent<HTMLInputElement>) => setStoreHost(e.target.value)} />
        </Field>
        <Field label="Port">
          <Input value={storePort} onChange={(e: ChangeEvent<HTMLInputElement>) => setStorePort(e.target.value)} />
        </Field>
        <Field label="Database">
          <Input
            value={storeDatabase}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setStoreDatabase(e.target.value)}
          />
        </Field>
        <Field label="User">
          <Input value={storeUser} onChange={(e: ChangeEvent<HTMLInputElement>) => setStoreUser(e.target.value)} />
        </Field>
        <Field label="SSL mode">
          <Input value={storeSslMode} onChange={(e: ChangeEvent<HTMLInputElement>) => setStoreSslMode(e.target.value)} />
        </Field>
        <Field label="Password">
          <SecretInput
            isConfigured={passwordConfigured}
            value={storePassword}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setStorePassword(e.target.value)}
            onReset={() => {
              setPasswordConfigured(false);
              setStorePassword('');
            }}
          />
        </Field>
        <Button onClick={save} disabled={saving}>
          Save
        </Button>
      </FieldSet>
      <p>Plugin id: {plugin.meta.id}</p>
      <p>
        Env <code>FORECAST_STORE_*</code> (not forwarded into plugin processes on Grafana 12.4+ by default) and
        grafana.ini <code>[plugin.eduardkolotushin-forecast-app]</code> override these fields in the overlay backend.
        Alerting QueryData is a separate process: set the same store on the Forecast datasource, or merge{' '}
        <code>[plugin.eduardkolotushin-forecast-datasource]</code>. See <code>conf/forecast.ini.template</code>.
      </p>
    </div>
  );
};

export default AppConfig;
