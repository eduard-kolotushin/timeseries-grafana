import React, { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { css, cx } from '@emotion/css';
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
import { livePanel, panelIdFromLocation } from './alertFromPanel';
import {
  absoluteDayBound,
  autoLookback,
  CivilDate,
  civilYmd,
  dashboardTimeZone,
  isExplicitAutoTrainRange,
} from './lookback';
import { TrainRangeCalendar } from './TrainRangeCalendar';
import { pickerLayout, placePicker, PickerLayout } from './pickerPlacement';
import { DEFAULT_FORECAST_OPTIONS, ForecastOptions, TrainTimeRange } from './types';

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

const QUICK_FUTURE: Array<{ from: string; to: string; display: string }> = [
  { from: 'now', to: 'now+5m', display: 'Next 5 minutes' },
  { from: 'now', to: 'now+15m', display: 'Next 15 minutes' },
  { from: 'now', to: 'now+30m', display: 'Next 30 minutes' },
  { from: 'now', to: 'now+1h', display: 'Next 1 hour' },
  { from: 'now', to: 'now+3h', display: 'Next 3 hours' },
  { from: 'now', to: 'now+6h', display: 'Next 6 hours' },
  { from: 'now', to: 'now+12h', display: 'Next 12 hours' },
  { from: 'now', to: 'now+24h', display: 'Next 24 hours' },
  { from: 'now', to: 'now+2d', display: 'Next 2 days' },
  { from: 'now', to: 'now+7d', display: 'Next 7 days' },
];

export type RangeEditorSettings = { kind?: 'train' | 'forecast' };

function pickerRaw(
  kind: 'train' | 'forecast',
  value: TrainTimeRange | undefined,
  auto: string,
  panelRange: TrainTimeRange | undefined
): TrainTimeRange {
  if (value != null && !isExplicitAutoTrainRange(value)) {
    return value;
  }
  if (kind === 'forecast') {
    return panelRange ?? { from: '', to: '' };
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

function timezoneLabel(timeZone: string): string {
  if (timeZone === 'browser' || timeZone === '') {
    return 'Browser time';
  }
  if (timeZone === 'utc') {
    return 'UTC';
  }
  return timeZone;
}

export const TrainRangeEditor = ({
  value,
  onChange,
  context,
  item,
}: StandardEditorProps<TrainTimeRange, RangeEditorSettings, ForecastOptions>) => {
  const styles = useStyles2(getStyles);
  const timeZone = dashboardTimeZone();
  const kind = item.settings?.kind ?? 'train';
  const auto = autoLookback(context.options?.model ?? DEFAULT_FORECAST_OPTIONS.model, context.options?.season);
  const panelRange = kind === 'forecast' ? livePanel(panelIdFromLocation()).time : undefined;
  const isAuto = value == null || isExplicitAutoTrainRange(value);
  const raw = pickerRaw(kind, value, auto, panelRange);
  const range = toPickerValue(raw, timeZone);

  const triggerRef = useRef<HTMLDivElement>(null);
  const pickerRef = useRef<HTMLDivElement>(null);
  const pickerId = useId();
  const [open, setOpen] = useState(false);
  const [placed, setPlaced] = useState(false);
  const [layout, setLayout] = useState<PickerLayout>('wide');
  const [anchor, setAnchor] = useState({ left: 0, top: 0 });
  const [from, setFrom] = useState(raw.from);
  const [to, setTo] = useState(raw.to);
  const [search, setSearch] = useState('');
  const [showCalendar, setShowCalendar] = useState(false);

  // Opening the picker reloads the draft fields from the panel's current value.
  // That is a render-time derivation, not an effect: setting state in an effect
  // made React render the popup twice for data it already had.
  const draftKey = [open, value?.from, value?.to, auto, kind, panelRange?.from, panelRange?.to].join('\u0000');
  const [draftedKey, setDraftedKey] = useState(draftKey);
  if (draftedKey !== draftKey) {
    setDraftedKey(draftKey);
    if (open) {
      const next = pickerRaw(kind, value, auto, panelRange);
      setFrom(next.from);
      setTo(next.to);
      setSearch('');
      setShowCalendar(false);
    }
  }

  /** Return focus only when the picker itself closed the interaction. */
  const close = useCallback((returnFocus = false) => {
    setOpen(false);
    setPlaced(false);
    setShowCalendar(false);
    if (returnFocus) {
      triggerRef.current?.querySelector<HTMLButtonElement>('button')?.focus();
    }
  }, []);

  const place = useCallback(() => {
    const btn = triggerRef.current?.getBoundingClientRect();
    const pop = pickerRef.current;
    if (!btn || !pop) {
      return;
    }
    const nextLayout = pickerLayout(btn.left, window.innerWidth, window.innerHeight);
    if (nextLayout !== layout) {
      // Re-measure after the calendar moves inside the popover.
      setLayout(nextLayout);
      return;
    }
    const width = pop.offsetWidth;
    const height = pop.offsetHeight;
    if (width === 0 || height === 0) {
      return;
    }
    const next = placePicker({
      layout,
      trigger: { left: btn.left, top: btn.top, bottom: btn.bottom },
      picker: { width, height },
      viewport: { width: window.innerWidth, height: window.innerHeight },
    });
    setAnchor((prev) => (prev.left === next.left && prev.top === next.top ? prev : next));
    setPlaced(true);
  }, [layout]);

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
        // Capture phase: the panel editor also listens for Escape and would exit
        // edit mode behind the open picker.
        event.preventDefault();
        event.stopPropagation();
        close(true);
      }
    };
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onKey, true);
    return () => {
      document.removeEventListener('mousedown', onDoc);
      document.removeEventListener('keydown', onKey, true);
    };
  }, [open, close]);

  useEffect(() => {
    if (!open || !placed) {
      return;
    }
    const frame = requestAnimationFrame(() => {
      pickerRef.current?.querySelector<HTMLElement>('input, button')?.focus();
    });
    return () => cancelAnimationFrame(frame);
  }, [open, placed]);

  const onPickerKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Escape') {
      event.stopPropagation();
      close(true);
      return;
    }
    if (event.key !== 'Tab') {
      return;
    }
    const focusable = pickerRef.current?.querySelectorAll<HTMLElement>(
      'button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])'
    );
    if (!focusable || focusable.length === 0) {
      return;
    }
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  };

  const apply = (next: TrainTimeRange) => {
    onChange({ from: next.from.trim(), to: next.to.trim() });
    close(true);
  };

  const toggleCalendar = () => {
    const next = !showCalendar;
    setShowCalendar(next);
    if (next) {
      requestAnimationFrame(() => {
        pickerRef.current?.querySelector<HTMLButtonElement>('table[aria-label*="calendar"] td button')?.focus();
      });
    }
  };

  const selectRange = (start: CivilDate, end: CivilDate) => {
    setFrom(absoluteDayBound(start, timeZone, false));
    setTo(absoluteDayBound(end, timeZone, true));
    setShowCalendar(false);
    requestAnimationFrame(() => pickerRef.current?.querySelector<HTMLInputElement>('input')?.focus());
  };

  const quick = (kind === 'forecast' ? QUICK_FUTURE : QUICK).filter((q) =>
    q.display.toLowerCase().includes(search.trim().toLowerCase())
  );
  const calFrom = civilYmd(parseDate(from, timeZone).valueOf(), timeZone);
  const calTo = civilYmd(parseDate(to, timeZone).valueOf(), timeZone);

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
          aria-haspopup="dialog"
          aria-expanded={open}
          aria-controls={open ? pickerId : undefined}
          onClick={() => (open ? close(false) : setOpen(true))}
        >
          <span className={styles.label}>
            <TimeRangeLabel
              value={range}
              timeZone={timeZone}
              placeholder={kind === 'forecast' ? 'Auto (panel range)' : 'Auto (by model)'}
            />
          </span>
        </Button>
      </div>
      {isAuto && (
        <div className={styles.hint}>
          {kind === 'forecast'
            ? `Auto (the panel’s visible range). Open to set From and To; use Auto in the picker to reset.`
            : `Auto (last ${auto} ending at the panel To). Open to set From and To; use Auto in the picker to reset.`}
        </div>
      )}
      {open &&
        createPortal(
          <div
            ref={pickerRef}
            id={pickerId}
            role="dialog"
            aria-label={kind === 'forecast' ? 'Forecast range' : 'Training period'}
            onKeyDown={onPickerKeyDown}
            className={cx(styles.picker, layout === 'stacked' && styles.pickerStack)}
            style={{ left: anchor.left, top: anchor.top, visibility: placed ? 'visible' : 'hidden' }}
          >
            {showCalendar && layout === 'wide' && (
              <div className={styles.calendarDock}>
                <TrainRangeCalendar from={calFrom} to={calTo} timeZone={timeZone} onSelect={selectRange} />
              </div>
            )}
            {showCalendar && layout === 'stacked' && (
              <div className={styles.calendarStack}>
                <TrainRangeCalendar from={calFrom} to={calTo} timeZone={timeZone} onSelect={selectRange} />
              </div>
            )}
            <div className={cx(styles.body, layout === 'stacked' && styles.bodyStack)}>
              <div className={cx(styles.absolute, layout === 'stacked' && styles.absoluteStack)}>
                <div className={styles.section}>Absolute time range ({timezoneLabel(timeZone)})</div>
                <Field label="From" noMargin className={styles.field}>
                  <div className={styles.inputRow}>
                    <div className={styles.inputGrow}>
                      <Input
                        value={from}
                        placeholder={kind === 'forecast' ? 'now' : 'now-7d'}
                        onChange={(e) => setFrom(e.currentTarget.value)}
                      />
                    </div>
                    <IconButton
                      name="calendar-alt"
                      type="button"
                      tooltip="Select range on calendar"
                      aria-expanded={showCalendar}
                      onClick={toggleCalendar}
                    />
                  </div>
                </Field>
                <Field label="To" noMargin className={styles.field}>
                  <div className={styles.inputRow}>
                    <div className={styles.inputGrow}>
                      <Input
                        value={to}
                        placeholder={kind === 'forecast' ? 'now+6h' : 'now'}
                        onChange={(e) => setTo(e.currentTarget.value)}
                      />
                    </div>
                    <IconButton
                      name="calendar-alt"
                      type="button"
                      tooltip="Select range on calendar"
                      aria-expanded={showCalendar}
                      onClick={toggleCalendar}
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
              <div className={cx(styles.quick, layout === 'stacked' && styles.quickStack)}>
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
      width: 440,
      boxSizing: 'border-box',
      padding: theme.spacing(1.5),
      background: theme.colors.background.elevated,
      border: `1px solid ${theme.colors.border.weak}`,
      borderRadius: theme.shape.radius.lg,
      boxShadow: theme.shadows.z3,
      overflow: 'visible',
    }),
    // The calendar moves inside the popover and the whole box scrolls when the
    // trigger is too close to the left edge for the docked layout.
    pickerStack: css({
      width: 'min(360px, calc(100vw - 16px))',
      maxHeight: 'calc(100vh - 16px)',
      overflowY: 'auto',
    }),
    calendarDock: css({
      position: 'absolute',
      right: '100%',
      top: 0,
      marginRight: theme.spacing(1),
      zIndex: 1,
    }),
    calendarStack: css({
      display: 'flex',
      justifyContent: 'center',
      marginBottom: theme.spacing(1.5),
    }),
    body: css({
      display: 'flex',
      flexWrap: 'nowrap',
      alignItems: 'flex-start',
      gap: theme.spacing(2),
    }),
    bodyStack: css({
      flexDirection: 'column',
    }),
    absolute: css({
      flex: '1 1 220px',
      minWidth: 200,
      overflow: 'visible',
    }),
    absoluteStack: css({
      width: '100%',
      minWidth: 0,
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
    quickStack: css({
      flex: '1 1 auto',
      width: '100%',
      borderLeft: 'none',
      borderTop: `1px solid ${theme.colors.border.weak}`,
      paddingLeft: 0,
      paddingTop: theme.spacing(1.5),
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
