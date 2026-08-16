import React, { Suspense, lazy } from 'react';
import { AppPlugin } from '@grafana/data';
import { getBackendSrv } from '@grafana/runtime';
import { LoadingPlaceholder } from '@grafana/ui';
import type { AppConfigProps, AppJsonData } from './components/AppConfig/AppConfig';
import { PLUGIN_HEALTH_URL } from './constants';

const App = lazy(() => import('./components/App/App'));
const LazyAppConfig = lazy(() => import('./components/AppConfig/AppConfig'));

const AppConfig = (props: AppConfigProps) => (
  <Suspense fallback={<LoadingPlaceholder text="" />}>
    <LazyAppConfig {...props} />
  </Suspense>
);

export const plugin = new AppPlugin<AppJsonData>().setRootPage(App).addConfigPage({
  title: 'Configuration',
  icon: 'cog',
  body: AppConfig,
  id: 'configuration',
});

void getBackendSrv()
  .get(PLUGIN_HEALTH_URL)
  .catch(() => undefined);
