import React, { useState } from 'react';
import { StandardEditorProps } from '@grafana/data';
import { locationService } from '@grafana/runtime';
import { Alert, Button } from '@grafana/ui';
import {
  absoluteRangeMs,
  alertingNewPath,
  alertingUidsFor,
  dashboardUidFromPath,
  forecastDatasourceUid,
  livePanel,
  panelIdFromLocation,
  panelSeries,
  REASON_DASHBOARD_NOT_SAVED,
  ruleDefaultsFromPanel,
} from './alertFromPanel';
import { dashboardTimeZone } from './lookback';
import { metricTargets } from './mixed';
import { ForecastOptions } from './types';

export const AlertEditor: React.FC<StandardEditorProps<unknown, unknown, ForecastOptions>> = ({ context }) => {
  const [reason, setReason] = useState<string | null>(null);

  const onClick = () => {
    const loc = locationService.getLocation();
    const panelId = panelIdFromLocation();
    const live = livePanel(panelId);
    const dashboardUid = live.dashboardUid || dashboardUidFromPath(loc.pathname);
    if (!dashboardUid || !Number.isFinite(panelId)) {
      setReason(REASON_DASHBOARD_NOT_SAVED);
      return;
    }
    const panel = live.panel ?? { id: panelId };
    // The rule's cacheKey is a hash of the panel's fit inputs, so building the defaults is
    // async. The panel's loaded frames decide which series exist: a stale `Alert series`
    // names nothing, and the builder answers a reason for it.
    const build = async () => {
      const result = await ruleDefaultsFromPanel({
        dashboardUid,
        panel,
        panelId,
        options: context.options,
        series: panelSeries(context.data, panel.targets),
        metricTargets: metricTargets(panel.targets ?? []),
        ...absoluteRangeMs(live.time, dashboardTimeZone()),
        alertingUids: alertingUidsFor(panel.targets ?? [], panel.datasource),
        forecastDatasourceUid: forecastDatasourceUid(panel.targets ?? []),
      });
      if (!result.ok) {
        setReason(result.reason);
        return;
      }
      setReason(null);
      locationService.push(alertingNewPath(result.defaults, loc.pathname + loc.search));
    };
    void build().catch((err: unknown) => {
      setReason(err instanceof Error ? err.message : String(err));
    });
  };

  return (
    <div>
      {reason && (
        <Alert title="Cannot create alert" severity="info">
          {reason}
        </Alert>
      )}
      <Button icon="bell" variant="secondary" type="button" onClick={onClick}>
        New alert rule
      </Button>
    </div>
  );
};
