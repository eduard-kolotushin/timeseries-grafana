import React, { ChangeEvent, useState } from 'react';
import { AppPluginMeta, PluginConfigPageProps } from '@grafana/data';
import { getBackendSrv } from '@grafana/runtime';
import { Alert, Button, Field, FieldSet, Input } from '@grafana/ui';
import { httpStatusFromUnknown, reasonFromUnknown } from '../../forecast-panel/reasons';
import { postScheduleDefault } from '../../forecast-panel/scheduleApi';
import { testIds } from '../testIds';

// The store keys stay in the type because provisioning writes them into this plugin's
// jsonData/secureJsonData; the page itself only reads jsonData.retrainCron/retrainTimezone.
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
        // The store is a deployment parameter (env / GF_PLUGIN_* / ini / provisioned jsonData), so this
        // page writes only the default retrain schedule. Spreading `json` first keeps every key this page
        // does not render — the store's, and retrainEnabled/retrainTick/grafanaUrl/grafanaToken — intact.
        jsonData: {
          ...json,
          retrainCron,
          retrainTimezone,
        },
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
        This page writes only <code>jsonData.retrainCron</code> / <code>jsonData.retrainTimezone</code>; every other
        setting comes from deployment configuration and is left untouched by a save.
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
