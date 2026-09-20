import React, { Suspense, lazy } from 'react';
import { AppPlugin } from '@grafana/data';
import { LoadingPlaceholder } from '@grafana/ui';
import type { AppConfigProps, ForecastStoreJsonData } from './components/AppConfig/AppConfig';

const App = lazy(() => import('./components/App/App'));
const LazyAppConfig = lazy(() => import('./components/AppConfig/AppConfig'));
const LazyRetrainSchedulesPage = lazy(() => import('./pages/RetrainSchedulesPage'));

const AppConfig = (props: AppConfigProps) => (
  <Suspense fallback={<LoadingPlaceholder text="" />}>
    <LazyAppConfig {...props} />
  </Suspense>
);

const RetrainSchedulesPage = () => (
  <Suspense fallback={<LoadingPlaceholder text="" />}>
    <LazyRetrainSchedulesPage />
  </Suspense>
);

// Configuration stays first: Grafana derives the default tab (/plugins/<id>) from configPages[0].id.
export const plugin = new AppPlugin<ForecastStoreJsonData>()
  .setRootPage(App)
  .addConfigPage({ title: 'Configuration', icon: 'cog', body: AppConfig, id: 'configuration' })
  .addConfigPage({ title: 'Retrain schedules', icon: 'sync', body: RetrainSchedulesPage, id: 'schedules' });
