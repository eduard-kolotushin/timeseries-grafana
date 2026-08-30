import React from 'react';
import { PluginPage } from '@grafana/runtime';
import { testIds } from '../components/testIds';

function Home() {
  return (
    <PluginPage>
      <div data-testid={testIds.pageOne.container}>
        <p>Add the Forecast overlay visualization to a dashboard panel and query any data source.</p>
        <p>
          The panel sends the series to this app&apos;s Go backend, which fits a model from{' '}
          <code>timeseries-forecast</code> and overlays the forecast window on the history.
        </p>
        <p>
          For Grafana alerting, add a <strong>Forecast</strong> datasource query (refId B) after training on the overlay.
          Query A stays the live metric. The forecast query Restores the saved snapshot; it does not re-query the metric
          datasource.
        </p>
        <p>The Docker Grafana sandbox (TestData demo dashboard) is the sibling repo timeseries-grafana-sandbox.</p>
      </div>
    </PluginPage>
  );
}

export default Home;
