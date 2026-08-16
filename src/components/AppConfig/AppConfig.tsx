import React, { ChangeEvent, useState } from 'react';
import { AppPluginMeta, PluginConfigPageProps } from '@grafana/data';
import { getBackendSrv } from '@grafana/runtime';
import { Button, Field, Input, Switch } from '@grafana/ui';
import { testIds } from '../testIds';
import { PLUGIN_HEALTH_URL } from '../../constants';

export type AppJsonData = {
  enabled?: boolean;
  druidBroker?: string;
  druidDatasource?: string;
  kafkaBrokers?: string;
  kafkaTopic?: string;
  lookback?: string;
  aheadMinutes?: number | string;
  interval?: string;
  calendar?: string;
};

export type AppConfigProps = PluginConfigPageProps<AppPluginMeta<AppJsonData>>;

const AppConfig = ({ plugin }: AppConfigProps) => {
  const json = plugin.meta.jsonData ?? {};
  const configured = Boolean(json.druidBroker && json.kafkaTopic);
  const [enabled, setEnabled] = useState(json.enabled === undefined ? configured : Boolean(json.enabled));
  const [druidBroker, setDruidBroker] = useState(json.druidBroker ?? '');
  const [druidDatasource, setDruidDatasource] = useState(json.druidDatasource ?? '');
  const [kafkaBrokers, setKafkaBrokers] = useState(json.kafkaBrokers ?? '');
  const [kafkaTopic, setKafkaTopic] = useState(json.kafkaTopic ?? '');
  const [lookback, setLookback] = useState(json.lookback ?? '336h');
  const [aheadMinutes, setAheadMinutes] = useState(String(json.aheadMinutes ?? 1));
  const [interval, setInterval] = useState(json.interval ?? '1m');
  const [calendar, setCalendar] = useState(json.calendar ?? '');
  const [saving, setSaving] = useState(false);
  const [status, setStatus] = useState('');

  const onSave = async () => {
    setSaving(true);
    setStatus('');
    try {
      await getBackendSrv().post(`/api/plugins/${plugin.meta.id}/settings`, {
        enabled: true,
        pinned: plugin.meta.pinned,
        jsonData: {
          enabled,
          druidBroker,
          druidDatasource,
          kafkaBrokers,
          kafkaTopic,
          lookback,
          aheadMinutes: Number(aheadMinutes) || 1,
          interval,
          calendar,
        },
      });
      const health = await getBackendSrv().get<{ message?: string }>(PLUGIN_HEALTH_URL);
      setStatus(health?.message ? `Saved. ${health.message}` : 'Saved. Publisher reloaded.');
    } catch (err) {
      setStatus(err instanceof Error ? err.message : 'save failed');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div data-testid={testIds.appConfig.form}>
      <p>
        Overlay panels need no extra settings. The minute-of-week baseline publisher reads a Druid table and writes one
        lead point per ready <code>metric_hash</code> to Kafka.
      </p>
      <Field label="Publish baselines">
        <Switch
          value={enabled}
          onChange={() => setEnabled(!enabled)}
          data-testid={testIds.appConfig.enabled}
        />
      </Field>
      <Field label="Druid broker URL" description="Example: http://druid-broker:8082">
        <Input
          value={druidBroker}
          onChange={(e: ChangeEvent<HTMLInputElement>) => setDruidBroker(e.target.value)}
          data-testid={testIds.appConfig.druidBroker}
        />
      </Field>
      <Field label="Druid datasource" description="Kafka-indexed table with metric_hash and metric_value">
        <Input
          value={druidDatasource}
          onChange={(e: ChangeEvent<HTMLInputElement>) => setDruidDatasource(e.target.value)}
          data-testid={testIds.appConfig.druidDatasource}
        />
      </Field>
      <Field label="Kafka brokers" description="Comma-separated, from the Grafana container">
        <Input
          value={kafkaBrokers}
          onChange={(e: ChangeEvent<HTMLInputElement>) => setKafkaBrokers(e.target.value)}
          data-testid={testIds.appConfig.kafkaBrokers}
        />
      </Field>
      <Field label="Baseline Kafka topic" description="Must not be the metrics topic Druid consumes">
        <Input
          value={kafkaTopic}
          onChange={(e: ChangeEvent<HTMLInputElement>) => setKafkaTopic(e.target.value)}
          data-testid={testIds.appConfig.kafkaTopic}
        />
      </Field>
      <Field label="Lookback" description="Eligibility span and fit window. Default 336h (2 weeks).">
        <Input
          value={lookback}
          onChange={(e: ChangeEvent<HTMLInputElement>) => setLookback(e.target.value)}
          data-testid={testIds.appConfig.lookback}
        />
      </Field>
      <Field label="Ahead minutes N" description="Publish last observed timestamp + N minutes">
        <Input
          type="number"
          value={aheadMinutes}
          onChange={(e: ChangeEvent<HTMLInputElement>) => setAheadMinutes(e.target.value)}
          data-testid={testIds.appConfig.aheadMinutes}
        />
      </Field>
      <Field label="Interval" description="How often the backend scans Druid">
        <Input
          value={interval}
          onChange={(e: ChangeEvent<HTMLInputElement>) => setInterval(e.target.value)}
          data-testid={testIds.appConfig.interval}
        />
      </Field>
      <Field label="Calendar" description="Empty or ru">
        <Input
          value={calendar}
          onChange={(e: ChangeEvent<HTMLInputElement>) => setCalendar(e.target.value)}
          data-testid={testIds.appConfig.calendar}
        />
      </Field>
      <Button onClick={onSave} disabled={saving} data-testid={testIds.appConfig.submit}>
        Save
      </Button>
      {status ? <p>{status}</p> : null}
    </div>
  );
};

export default AppConfig;
