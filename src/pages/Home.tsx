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
          Mixed query A (metric) plus query B (Forecast datasource) is supported on the overlay. The overlay fits and
          plots the metric only; Forecast datasource frames are not fitted or drawn there. To plot Restore next to the
          metric, use Grafana Time series. After training, fill the Forecast query editor yourself (model, train
          strings, series name, source query). Copy source from query A is optional and copies the source fingerprint
          only.
        </p>
        <p>
          For Grafana alerting, query A stays the live metric and query B Restores the saved snapshot. Save the dashboard,
          then use overlay options <strong>New alert rule</strong> to open Grafana&apos;s alerting editor with the live
          panel queries (including Mixed Forecast rows). Grafana still shows the data-pane Alert tab only for Time series
          and Graph. Panel menu More → New alert rule and Alerting → New alert rule remain.
        </p>
        <p>The Docker Grafana sandbox (TestData demo dashboard) is the sibling repo timeseries-grafana-sandbox.</p>
      </div>
    </PluginPage>
  );
}

export default Home;
