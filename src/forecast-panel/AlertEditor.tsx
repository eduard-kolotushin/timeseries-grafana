import React, { useState } from 'react';
import { rangeUtil, StandardEditorProps } from '@grafana/data';
import { locationService } from '@grafana/runtime';
import { Alert, Button } from '@grafana/ui';
import {
  alertingNewPath,
  alertingUidsFor,
  asSaveModel,
  DashboardSaveModel,
  dashboardTimeFromScene,
  dashboardUidFromPath,
  DEFAULT_RELATIVE_TIME_RANGE,
  findPanel,
  findQueryRunnerState,
  LivePanel,
  pickLivePanel,
  REASON_DASHBOARD_NOT_SAVED,
  RelativeTimeRange,
  ruleFormDefaultsFromPanel,
} from './alertFromPanel';

function liveSaveModel(): DashboardSaveModel | undefined {
  const runtime = (window as Window & { grafanaRuntime?: { getDashboardSaveModel?: () => unknown } }).grafanaRuntime;
  try {
    return asSaveModel(runtime?.getDashboardSaveModel?.());
  } catch {
    return undefined;
  }
}

function liveFromScene(panelId: number): { dashboardUid?: string; panel?: LivePanel; time?: { from: string; to: string } } {
  const dash = (window as Window & { __grafanaSceneContext?: unknown }).__grafanaSceneContext as
    | {
        state?: {
          uid?: string;
          $timeRange?: unknown;
          editPanel?: { state?: { panelRef?: { resolve?: () => unknown } } };
        };
      }
    | undefined;
  const viz = dash?.state?.editPanel?.state?.panelRef?.resolve?.();
  const runner = findQueryRunnerState(viz);
  const title =
    viz && typeof viz === 'object' && 'state' in viz
      ? String((viz as { state?: { title?: string } }).state?.title ?? '')
      : '';
  return {
    dashboardUid: dash?.state?.uid,
    time: dashboardTimeFromScene(dash),
    panel: {
      id: panelId,
      title,
      targets: runner?.queries,
      datasource: runner?.datasource,
      maxDataPoints: runner?.maxDataPoints,
    },
  };
}

function relativeFromDashboardTime(time?: { from: string; to: string }): RelativeTimeRange {
  if (!time?.from || !time?.to) {
    return DEFAULT_RELATIVE_TIME_RANGE;
  }
  try {
    return rangeUtil.timeRangeToRelative(rangeUtil.convertRawToRange(time));
  } catch {
    return DEFAULT_RELATIVE_TIME_RANGE;
  }
}

export const AlertEditor: React.FC<StandardEditorProps> = () => {
  const [reason, setReason] = useState<string | null>(null);

  const onClick = () => {
    const loc = locationService.getLocation();
    const panelId = Number(locationService.getSearchObject().editPanel);
    const model = liveSaveModel();
    const fromScene = liveFromScene(panelId);
    const dashboardUid = model?.uid || fromScene.dashboardUid || dashboardUidFromPath(loc.pathname);
    if (!dashboardUid || !Number.isFinite(panelId)) {
      setReason(REASON_DASHBOARD_NOT_SAVED);
      return;
    }
    const panel = pickLivePanel(findPanel(model?.panels, panelId), fromScene.panel, panelId);
    const result = ruleFormDefaultsFromPanel({
      dashboardUid,
      panel,
      panelId,
      relativeTimeRange: relativeFromDashboardTime(fromScene.time ?? model?.time),
      alertingUids: alertingUidsFor(panel.targets ?? [], panel.datasource),
    });
    if (!result.ok) {
      setReason(result.reason);
      return;
    }
    setReason(null);
    locationService.push(alertingNewPath(result.defaults, loc.pathname + loc.search));
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
