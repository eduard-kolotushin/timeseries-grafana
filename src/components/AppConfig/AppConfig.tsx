import React, { ChangeEvent, useState } from 'react';
import { AppPluginMeta, PluginConfigPageProps } from '@grafana/data';
import { getBackendSrv } from '@grafana/runtime';
import { Alert, Button, Field, FieldSet, Input, SecretInput } from '@grafana/ui';
import { httpStatusFromUnknown, reasonFromUnknown } from '../../forecast-panel/reasons';
import { postScheduleDefault } from '../../forecast-panel/scheduleApi';
import { testIds } from '../testIds';

export type ForecastStoreJsonData = {
  storeHost?: string;
  storePort?: number | string;
  storeDatabase?: string;
  storeUser?: string;
  storeSslMode?: string;
  retrainCron?: string;
  retrainTimezone?: string;
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
  const [retrainCron, setRetrainCron] = useState(json.retrainCron ?? '0 3 * * *');
  const [retrainTimezone, setRetrainTimezone] = useState(json.retrainTimezone ?? 'UTC');
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  const save = async () => {
    setSaving(true);
    try {
      // The backend is the only place that can prove the default cron parses and its zone
      // exists. An unvalidated default silently stops every new panel row from being
      // scheduled, so a rejected default blocks the save. Not being able to ask (no Admin
      // role, no reachable backend) keeps the previous save behaviour instead.
      try {
        await postScheduleDefault({ cron: retrainCron, timezone: retrainTimezone });
      } catch (e) {
        if (httpStatusFromUnknown(e) === 400) {
          throw e;
        }
      }
      await getBackendSrv().post(`/api/plugins/${plugin.meta.id}/settings`, {
        enabled: true,
        pinned: plugin.meta.pinned,
        // Spread first: settings this page does not render (retrainEnabled, retrainTick,
        // grafanaUrl, grafanaToken, …) must survive a save.
        jsonData: {
          ...json,
          storeHost,
          storePort: Number(storePort) || 5432,
          storeDatabase,
          storeUser,
          storeSslMode,
          retrainCron,
          retrainTimezone,
        },
        secureJsonData: passwordConfigured && !storePassword ? {} : { storePassword },
      });
      setSaveError(null);
    } catch (e) {
      setSaveError(reasonFromUnknown(e));
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
      </FieldSet>
      <FieldSet label="Default retrain schedule">
        <p>
          Written to <code>jsonData.retrainCron</code> / <code>jsonData.retrainTimezone</code> by the Save button below.
          A model with no row of its own uses this cron and timezone; per-model rows are edited on the{' '}
          <strong>Retrain schedules</strong> tab.
        </p>
        <Field label="Cron" description="5-field cron, or @hourly / @daily. The backend parses it per row.">
          <Input value={retrainCron} onChange={(e: ChangeEvent<HTMLInputElement>) => setRetrainCron(e.target.value)} />
        </Field>
        <Field label="Timezone" description="IANA zone, e.g. UTC or Europe/Moscow.">
          <Input
            value={retrainTimezone}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setRetrainTimezone(e.target.value)}
          />
        </Field>
      </FieldSet>
      <p>Plugin id: {plugin.meta.id}</p>
      <p>
        Env <code>FORECAST_STORE_*</code> (not forwarded into plugin processes on Grafana 12.4+ by default) and
        grafana.ini <code>[plugin.eduardkolotushin-forecast-app]</code> override these fields in the overlay backend.
        Alerting QueryData is a separate process: set the same store on the Forecast datasource, or merge{' '}
        <code>[plugin.eduardkolotushin-forecast-datasource]</code>. See <code>conf/forecast.ini.template</code>.
      </p>
      {saveError && (
        <Alert title="Settings not saved" severity="error">
          {saveError}
        </Alert>
      )}
      <Button onClick={save} disabled={saving}>
        Save
      </Button>
    </div>
  );
};

export default AppConfig;
