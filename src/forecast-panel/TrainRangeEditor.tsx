import React, { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { css } from '@emotion/css';
import {
  dateTime,
  dateTimeFormat,
  dateTimeParse,
  DateTime,
  GrafanaTheme2,
  rangeUtil,
  StandardEditorProps,
  TimeRange,
} from '@grafana/data';
import { Button, Field, FilterInput, IconButton, Input, Stack, TimeRangeLabel, useStyles2 } from '@grafana/ui';
import { autoLookback, isExplicitAutoTrainRange } from './lookback';
import { TrainRangeCalendar } from './TrainRangeCalendar';
import { ForecastOptions, TrainTimeRange } from './types';

const QUICK: Array<{ from: string; to: string; display: string }> = [
  { from: 'now-5m', to: 'now', display: 'Last 5 minutes' },
  { from: 'now-15m', to: 'now', display: 'Last 15 minutes' },
  { from: 'now-30m', to: 'now', display: 'Last 30 minutes' },
  { from: 'now-1h', to: 'now', display: 'Last 1 hour' },
  { from: 'now-3h', to: 'now', display: 'Last 3 hours' },
  { from: 'now-6h', to: 'now', display: 'Last 6 hours' },
  { from: 'now-12h', to: 'now', display: 'Last 12 hours' },
  { from: 'now-24h', to: 'now', display: 'Last 24 hours' },
  { from: 'now-2d', to: 'now', display: 'Last 2 days' },
  { from: 'now-7d', to: 'now', display: 'Last 7 days' },
  { from: 'now-14d', to: 'now', display: 'Last 14 days' },
  { from: 'now-21d', to: 'now', display: 'Last 21 days' },
  { from: 'now-30d', to: 'now', display: 'Last 30 days' },
  { from: 'now-90d', to: 'now', display: 'Last 90 days' },
  { from: 'now-6M', to: 'now', display: 'Last 6 months' },
  { from: 'now-1y', to: 'now', display: 'Last 1 year' },
];

function pickerRaw(
  value: TrainTimeRange | undefined,
  lookback: string | undefined,
  auto: string
): TrainTimeRange {
  if (value != null && !isExplicitAutoTrainRange(value)) {
    return value;
  }
  const override = lookback?.trim();
  if (value == null && override && override.toLowerCase() !== 'auto') {
    const duration = /^\d+(\.\d+)?$/.test(override) ? `${override}d` : override;
    return { from: `now-${duration}`, to: 'now' };
  }
  return { from: `now-${auto}`, to: 'now' };
}

function toPickerValue(raw: TrainTimeRange, timeZone: string): TimeRange {
  try {
    return rangeUtil.convertRawToRange({ from: raw.from, to: raw.to }, timeZone);
  } catch {
    return { from: dateTime(null), to: dateTime(null), raw: { from: '', to: '' } };
  }
}

function parseDate(raw: string, timeZone: string): DateTime {
  try {
    const dt = dateTimeParse(raw, { timeZone });
    return dt.isValid() ? dt : dateTime();
  } catch {
    return dateTime();
  }
}

function formatDate(d: Date, timeZone: string, endOfDay: boolean): string {
  const dt = endOfDay ? dateTime(d).endOf('day') : dateTime(d).startOf('day');
  return dateTimeFormat(dt, { timeZone, format: 'YYYY-MM-DD HH:mm:ss' });
}

export const TrainRangeEditor = ({
  value,
  onChange,
  context,
}: StandardEditorProps<TrainTimeRange, unknown, ForecastOptions>) => {
  const styles = useStyles2(getStyles);
  const timeZone = 'browser';
  const auto = autoLookback(context.options?.model ?? 'holt', context.options?.season);
  const isAuto = value == null || isExplicitAutoTrainRange(value);
  const raw = pickerRaw(value, context.options?.lookback, auto);
  const range = toPickerValue(raw, timeZone);

  const triggerRef = useRef<HTMLDivElement>(null);
  const pickerRef = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [placed, setPlaced] = useState(false);
  const [anchor, setAnchor] = useState({ left: 0, top: 0 });
  const [from, setFrom] = useState(raw.from);
  const [to, setTo] = useState(raw.to);
  const [search, setSearch] = useState('');
  const [showCalendar, setShowCalendar] = useState(false);

  const close = () => {
    setOpen(false);
    setPlaced(false);
    setShowCalendar(false);
  };

  const place = useCallback(() => {
    const btn = triggerRef.current?.getBoundingClientRect();
    const pop = pickerRef.current;
    if (!btn || !pop || pop.offsetWidth === 0) {
      return;
    }
    const gap = 4;
    const next = {
      left: Math.max(8, btn.left - pop.offsetWidth - gap),
      top: Math.max(8, btn.top - pop.offsetHeight - gap),
    };
    setAnchor((prev) => (prev.left === next.left && prev.top === next.top ? prev : next));
    setPlaced(true);
  }, []);

  useEffect(() => {
    if (!open) {
      return;
    }
    const next = pickerRaw(value, context.options?.lookback, auto);
    setFrom(next.from);
    setTo(next.to);
    setSearch('');
    setShowCalendar(false);
  }, [open, value, context.options?.lookback, auto]);

  useLayoutEffect(() => {
    if (!open) {
      return;
    }
    place();
    const frame = requestAnimationFrame(place);
    window.addEventListener('resize', place);
    window.addEventListener('scroll', place, true);
    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener('resize', place);
      window.removeEventListener('scroll', place, true);
    };
  }, [open, showCalendar, place]);

  useEffect(() => {
    if (!open) {
      return;
    }
    const onDoc = (event: MouseEvent) => {
      const target = event.target as Node | null;
      if (triggerRef.current?.contains(target) || pickerRef.current?.contains(target)) {
        return;
      }
      close();
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        close();
      }
    };
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDoc);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  const apply = (next: TrainTimeRange) => {
    onChange({ from: next.from.trim(), to: next.to.trim() });
    close();
  };

  const quick = QUICK.filter((q) => q.display.toLowerCase().includes(search.trim().toLowerCase()));
  const calFrom = parseDate(from, timeZone).toDate();
  const calTo = parseDate(to, timeZone).toDate();

  const tooltip = isAuto
    ? undefined
    : `${dateTimeFormat(range.from, { timeZone })} to ${dateTimeFormat(range.to, { timeZone })}`;

  return (
    <div className={styles.wrap}>
      <div ref={triggerRef} className={styles.triggerWrap}>
        <Button
          variant="secondary"
          icon="clock-nine"
          fullWidth
          className={styles.trigger}
          tooltip={tooltip}
          onClick={() => (open ? close() : setOpen(true))}
        >
          <span className={styles.label}>
            <TimeRangeLabel value={range} timeZone={timeZone} placeholder="Auto (by model)" />
          </span>
        </Button>
      </div>
      {isAuto && (
        <div className={styles.hint}>
          Auto (last {auto} ending at the panel To). Open to set From and To; use Auto in the picker to reset.
        </div>
      )}
      {open &&
        createPortal(
          <div
            ref={pickerRef}
            className={styles.picker}
            style={{ left: anchor.left, top: anchor.top, visibility: placed ? 'visible' : 'hidden' }}
          >
            {showCalendar && (
              <div className={styles.calendarDock}>
                <TrainRangeCalendar
                  from={calFrom}
                  to={calTo}
                  onSelect={(start, end) => {
                    setFrom(formatDate(start, timeZone, false));
                    setTo(formatDate(end, timeZone, true));
                    setShowCalendar(false);
                  }}
                />
              </div>
            )}
            <div className={styles.body}>
              <div className={styles.absolute}>
                <div className={styles.section}>Absolute time range</div>
                <Field label="From" noMargin className={styles.field}>
                  <div className={styles.inputRow}>
                    <div className={styles.inputGrow}>
                      <Input value={from} placeholder="now-7d" onChange={(e) => setFrom(e.currentTarget.value)} />
                    </div>
                    <IconButton
                      name="calendar-alt"
                      type="button"
                      tooltip="Select range on calendar"
                      onClick={() => setShowCalendar((c) => !c)}
                    />
                  </div>
                </Field>
                <Field label="To" noMargin className={styles.field}>
                  <div className={styles.inputRow}>
                    <div className={styles.inputGrow}>
                      <Input value={to} placeholder="now" onChange={(e) => setTo(e.currentTarget.value)} />
                    </div>
                    <IconButton
                      name="calendar-alt"
                      type="button"
                      tooltip="Select range on calendar"
                      onClick={() => setShowCalendar((c) => !c)}
                    />
                  </div>
                </Field>
                <Stack direction="row" gap={1}>
                  <Button onClick={() => apply({ from, to })}>Apply time range</Button>
                  <Button variant="secondary" onClick={() => apply({ from: '', to: '' })}>
                    Auto
                  </Button>
                </Stack>
              </div>
              <div className={styles.quick}>
                <div className={styles.section}>Quick ranges</div>
                <FilterInput value={search} placeholder="Search quick ranges" onChange={setSearch} />
                <div className={styles.quickList}>
                  {quick.map((q) => (
                    <Button
                      key={q.display}
                      fill="text"
                      variant="secondary"
                      fullWidth
                      className={styles.quickItem}
                      onClick={() => apply({ from: q.from, to: q.to })}
                    >
                      {q.display}
                    </Button>
                  ))}
                </div>
              </div>
            </div>
          </div>,
          document.body
        )}
    </div>
  );
};

function getStyles(theme: GrafanaTheme2) {
  return {
    wrap: css({
      width: '100%',
      maxWidth: '100%',
      minWidth: 0,
    }),
    triggerWrap: css({
      width: '100%',
      maxWidth: '100%',
      minWidth: 0,
    }),
    trigger: css({
      justifyContent: 'flex-start',
      width: '100%',
      maxWidth: '100%',
      minWidth: '0 !important',
      overflow: 'hidden',
      '& > span': {
        minWidth: 0,
        maxWidth: '100%',
        overflow: 'hidden',
        flex: 1,
      },
    }),
    label: css({
      display: 'block',
      flex: 1,
      minWidth: 0,
      overflow: 'hidden',
      textOverflow: 'ellipsis',
      whiteSpace: 'nowrap',
      textAlign: 'left',
      '& > *': {
        display: 'block',
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
      },
    }),
    hint: css({
      marginTop: theme.spacing(0.5),
      color: theme.colors.text.secondary,
      fontSize: theme.typography.bodySmall.fontSize,
    }),
    picker: css({
      position: 'fixed',
      zIndex: theme.zIndex.modal,
      padding: theme.spacing(1.5),
      background: theme.colors.background.elevated,
      border: `1px solid ${theme.colors.border.weak}`,
      borderRadius: theme.shape.radius.lg,
      boxShadow: theme.shadows.z3,
      overflow: 'visible',
    }),
    calendarDock: css({
      position: 'absolute',
      right: '100%',
      top: 0,
      marginRight: theme.spacing(1),
      zIndex: 1,
    }),
    body: css({
      display: 'flex',
      flexWrap: 'nowrap',
      alignItems: 'flex-start',
      gap: theme.spacing(2),
    }),
    absolute: css({
      flex: '1 1 220px',
      minWidth: 200,
      overflow: 'visible',
    }),
    field: css({
      marginBottom: theme.spacing(1),
    }),
    inputRow: css({
      display: 'flex',
      flexDirection: 'row',
      alignItems: 'center',
      gap: theme.spacing(0.5),
    }),
    inputGrow: css({
      flex: 1,
      minWidth: 0,
      overflow: 'visible',
      '& > div': {
        width: '100%',
      },
    }),
    quick: css({
      flex: '0 0 180px',
      width: 180,
      display: 'flex',
      flexDirection: 'column',
      gap: theme.spacing(0.5),
      borderLeft: `1px solid ${theme.colors.border.weak}`,
      paddingLeft: theme.spacing(1.5),
    }),
    section: css({
      fontWeight: theme.typography.fontWeightMedium,
      marginBottom: theme.spacing(0.5),
    }),
    quickList: css({
      maxHeight: 148,
      overflowY: 'auto',
    }),
    quickItem: css({
      justifyContent: 'flex-start',
    }),
  };
}
