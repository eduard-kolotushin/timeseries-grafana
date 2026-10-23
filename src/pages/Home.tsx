import React from 'react';
import { css } from '@emotion/css';
import { config, PluginPage } from '@grafana/runtime';
import { LinkButton, Stack, useStyles2 } from '@grafana/ui';
import { testIds } from '../components/testIds';

function Home() {
  const styles = useStyles2((theme) => ({
    body: css({
      maxWidth: 760,
      '& h2': {
        marginTop: theme.spacing(3),
        marginBottom: theme.spacing(1),
      },
      '& ol': {
        paddingLeft: theme.spacing(3),
      },
    }),
  }));

  return (
    <PluginPage>
      <div data-testid={testIds.pageOne.container} className={styles.body}>
        <p>
          Overlay a forecast on any dashboard panel. The panel sends each numeric series to this app&apos;s Go backend;
          the fit is stored in Postgres and reused until you retrain.
        </p>
        <h2>Start here</h2>
        <ol>
          <li>
            Add the <strong>Forecast overlay</strong> panel to a dashboard and query your data source.
          </li>
          <li>
            Set the training window (independent of the dashboard range) and the forecast window. Auto works for most
            panels.
          </li>
          <li>
            Open <strong>Training → Baseline keys</strong> to copy a series&apos; cache key for a Forecast query or an
            alert rule. A Forecast query restores that snapshot by key alone.
          </li>
        </ol>
        <h2>Next</h2>
        <Stack direction="row" gap={2} wrap="wrap">
          <LinkButton
            variant="secondary"
            href={`${config.appSubUrl}/plugins/eduardkolotushin-forecast-app?page=configuration`}
          >
            Configure retrain defaults
          </LinkButton>
          <LinkButton
            variant="secondary"
            href={`${config.appSubUrl}/plugins/eduardkolotushin-forecast-app?page=schedules`}
          >
            Review retrain schedules
          </LinkButton>
          <LinkButton variant="secondary" href={`${config.appSubUrl}/dashboards`}>
            Open dashboards
          </LinkButton>
        </Stack>
        <h2>Mixed queries and alerting</h2>
        <p>
          The overlay fits and plots metric targets only; Forecast datasource frames are ignored there. To plot a
          restored snapshot next to the metric, use Grafana&apos;s <strong>Time series</strong> panel.
        </p>
        <p>
          For alerting, save the dashboard, then choose <strong>New alert rule</strong> in the overlay options. It
          copies the metric queries and adds one key-only Forecast query.
        </p>
      </div>
    </PluginPage>
  );
}

export default Home;
