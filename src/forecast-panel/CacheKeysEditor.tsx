import React, { useEffect, useState } from 'react';
import { GrafanaTheme2, StandardEditorProps } from '@grafana/data';
import { ClipboardButton, useStyles2 } from '@grafana/ui';
import { css } from '@emotion/css';
import { absoluteRangeMs, livePanel, panelIdFromLocation, panelSeries } from './alertFromPanel';
import { BaselineKey, baselineKeys } from './baselineKeys';
import { DEFAULT_FORECAST_OPTIONS, ForecastOptions } from './types';

type Computed = { signature: string; rows: BaselineKey[] };

/**
 * Read-only list of the cache keys this panel trains, one per visible series. A key is
 * what a Forecast datasource query or a hand-built alert rule needs: the snapshot behind
 * it already holds the model and the training window. Nothing is stored (the path is a
 * placeholder), and the keys are computed with the panel's own targets, options and
 * visible range — the same call the overlay makes.
 */
export const CacheKeysEditor: React.FC<StandardEditorProps<unknown, unknown, ForecastOptions>> = ({ context }) => {
  const styles = useStyles2(getStyles);
  const options = context.options ?? DEFAULT_FORECAST_OPTIONS;
  const panelId = panelIdFromLocation();
  const live = livePanel(panelId);
  const targets = live.panel?.targets ?? [];
  const series = panelSeries(context.data, targets);
  const range = absoluteRangeMs(live.time);
  const names = series.map((s) => s.name);
  const ready = names.length > 0 && targets.length > 0;
  // The live scene rebuilds its objects every render; this string is the value set the
  // computation reads, so only a real change re-runs it.
  const signature = JSON.stringify({ targets, names, options, range });
  const [computed, setComputed] = useState<Computed | null>(null);

  useEffect(() => {
    if (!ready) {
      return;
    }
    let cancelled = false;
    void baselineKeys({
      targets,
      names,
      options,
      visibleFromMs: range.visibleFromMs,
      visibleToMs: range.visibleToMs,
    }).then((rows) => {
      if (!cancelled) {
        setComputed({ signature, rows });
      }
    });
    return () => {
      cancelled = true;
    };
    // `signature` carries every value read here; the objects themselves are rebuilt each render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [signature, ready]);

  const rows = computed?.signature === signature ? computed.rows : null;
  if (!ready) {
    return <div className={styles.hint}>Load the panel and let it find series to list the keys it trains.</div>;
  }
  if (rows == null) {
    return <div className={styles.hint}>Computing keys…</div>;
  }
  return (
    <div className={styles.list}>
      {rows.map((row) => (
        <div key={row.name} className={styles.row}>
          <div className={styles.head}>
            <span className={styles.name} title={row.name}>
              {row.name}
            </span>
            <ClipboardButton
              getText={() => row.key}
              icon="copy"
              size="sm"
              variant="secondary"
              fill="text"
              tooltip="Copy key"
              aria-label={`Copy cache key for ${row.name}`}
            />
          </div>
          <code className={styles.key}>{row.key}</code>
        </div>
      ))}
    </div>
  );
};

function getStyles(theme: GrafanaTheme2) {
  return {
    hint: css({
      color: theme.colors.text.secondary,
      fontSize: theme.typography.bodySmall.fontSize,
    }),
    list: css({
      display: 'flex',
      flexDirection: 'column',
      gap: theme.spacing(1),
      width: '100%',
    }),
    // One series per block: the name and its copy button share a line, and the key gets
    // the full width of the pane so it can wrap instead of overflowing.
    row: css({
      display: 'flex',
      flexDirection: 'column',
      gap: theme.spacing(0.25),
      minWidth: 0,
    }),
    head: css({
      display: 'flex',
      alignItems: 'center',
      gap: theme.spacing(0.5),
      minWidth: 0,
    }),
    name: css({
      flex: '1 1 auto',
      minWidth: 0,
      overflow: 'hidden',
      textOverflow: 'ellipsis',
      whiteSpace: 'nowrap',
      fontWeight: theme.typography.fontWeightMedium,
    }),
    // Grafana's global stylesheet pins `code` to nowrap, so whiteSpace has to be reset
    // for a 64-char key to wrap inside the narrow options pane.
    key: css({
      display: 'block',
      width: '100%',
      whiteSpace: 'normal',
      overflowWrap: 'anywhere',
      wordBreak: 'break-all',
      fontFamily: theme.typography.fontFamilyMonospace,
      fontSize: theme.typography.bodySmall.fontSize,
      color: theme.colors.text.secondary,
      userSelect: 'all',
    }),
  };
}
