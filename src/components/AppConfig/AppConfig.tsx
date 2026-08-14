import React from 'react';
import { AppPluginMeta, PluginConfigPageProps } from '@grafana/data';
import { testIds } from '../testIds';

export interface AppConfigProps extends PluginConfigPageProps<AppPluginMeta<{}>> {}

const AppConfig = ({ plugin }: AppConfigProps) => {
  return (
    <div data-testid={testIds.appConfig.submit}>
      <p>
        No extra API settings. Enable this app, then use the <strong>Forecast overlay</strong> panel on a dashboard.
      </p>
      <p>Plugin id: {plugin.meta.id}</p>
    </div>
  );
};

export default AppConfig;
