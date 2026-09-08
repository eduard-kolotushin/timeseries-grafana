import React, { useMemo, useState } from 'react';
import { css, cx } from '@emotion/css';
import { dateTimeFormat, dateTimeParse, GrafanaTheme2 } from '@grafana/data';
import { Button, IconButton, useStyles2 } from '@grafana/ui';
import { CivilDate } from './lookback';

const WEEKDAYS = ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su'];

function pad2(n: number): string {
  return n.toString().padStart(2, '0');
}

function civilKey(d: CivilDate): number {
  return d.year * 10000 + d.month * 100 + d.day;
}

function isSameDay(a: CivilDate, b: CivilDate): boolean {
  return a.year === b.year && a.month === b.month && a.day === b.day;
}

function between(d: CivilDate, a: CivilDate, b: CivilDate): boolean {
  const t = civilKey(d);
  return t >= Math.min(civilKey(a), civilKey(b)) && t <= Math.max(civilKey(a), civilKey(b));
}

function addMonths(year: number, month: number, n: number): { year: number; month: number } {
  const i = year * 12 + (month - 1) + n;
  return { year: Math.floor(i / 12), month: (i % 12) + 1 };
}

/** Monday-first month grid in the Gregorian calendar (civil dates, timezone-independent). */
export function monthCells(year: number, month: number): CivilDate[] {
  const firstWeekday = new Date(Date.UTC(year, month - 1, 1)).getUTCDay();
  const lead = (firstWeekday + 6) % 7;
  const start = new Date(Date.UTC(year, month - 1, 1 - lead));
  const cells: CivilDate[] = [];
  for (let i = 0; i < 42; i++) {
    const t = new Date(start.getTime() + i * 86_400_000);
    cells.push({ year: t.getUTCFullYear(), month: t.getUTCMonth() + 1, day: t.getUTCDate() });
  }
  return cells;
}

function monthTitle(year: number, month: number, timeZone: string): string {
  try {
    const dt = dateTimeParse(`${year}-${pad2(month)}-01 00:00:00`, { timeZone });
    if (dt.isValid()) {
      return dateTimeFormat(dt, { timeZone, format: 'MMMM YYYY' });
    }
  } catch {
    // fall through
  }
  return `${year}-${pad2(month)}`;
}

export function TrainRangeCalendar({
  from,
  to,
  timeZone,
  onSelect,
}: {
  from: CivilDate;
  to: CivilDate;
  timeZone: string;
  onSelect: (from: CivilDate, to: CivilDate) => void;
}) {
  const styles = useStyles2(getStyles);
  const [cursor, setCursor] = useState(() => ({ year: from.year, month: from.month }));
  const [picking, setPicking] = useState<CivilDate | null>(null);
  const cells = useMemo(() => monthCells(cursor.year, cursor.month), [cursor.year, cursor.month]);
  const title = monthTitle(cursor.year, cursor.month, timeZone);

  const onDay = (d: CivilDate) => {
    if (!picking) {
      setPicking(d);
      return;
    }
    const a = civilKey(picking) <= civilKey(d) ? picking : d;
    const b = civilKey(picking) <= civilKey(d) ? d : picking;
    onSelect(a, b);
    setPicking(null);
  };

  return (
    <div className={styles.wrap}>
      <div className={styles.header}>
        <IconButton
          name="angle-left"
          tooltip="Previous month"
          onClick={() => setCursor((c) => addMonths(c.year, c.month, -1))}
        />
        <div className={styles.title}>{title}</div>
        <IconButton
          name="angle-right"
          tooltip="Next month"
          onClick={() => setCursor((c) => addMonths(c.year, c.month, 1))}
        />
      </div>
      <div className={styles.weekdays}>
        {WEEKDAYS.map((d) => (
          <div key={d}>{d}</div>
        ))}
      </div>
      <div className={styles.grid}>
        {cells.map((d) => {
          const inMonth = d.month === cursor.month;
          const selected = picking
            ? isSameDay(d, picking)
            : isSameDay(d, from) || isSameDay(d, to) || between(d, from, to);
          const edge = picking ? isSameDay(d, picking) : isSameDay(d, from) || isSameDay(d, to);
          return (
            <button
              key={civilKey(d)}
              type="button"
              className={cx(styles.day, !inMonth && styles.outside, selected && styles.range, edge && styles.edge)}
              onClick={() => onDay(d)}
            >
              {d.day}
            </button>
          );
        })}
      </div>
      <div className={styles.hint}>{picking ? 'Click the end date' : 'Click start date, then end date'}</div>
      <Button size="sm" variant="secondary" fill="outline" onClick={() => setPicking(null)}>
        Reset selection
      </Button>
    </div>
  );
}

function getStyles(theme: GrafanaTheme2) {
  return {
    wrap: css({
      padding: theme.spacing(1),
      border: `1px solid ${theme.colors.border.weak}`,
      borderRadius: theme.shape.radius.default,
      background: theme.colors.background.elevated,
      boxShadow: theme.shadows.z3,
      width: 240,
    }),
    header: css({
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'space-between',
      marginBottom: theme.spacing(0.5),
    }),
    title: css({
      fontWeight: theme.typography.fontWeightMedium,
      fontSize: theme.typography.bodySmall.fontSize,
    }),
    weekdays: css({
      display: 'grid',
      gridTemplateColumns: 'repeat(7, 1fr)',
      textAlign: 'center',
      color: theme.colors.text.secondary,
      fontSize: 11,
      marginBottom: theme.spacing(0.5),
    }),
    grid: css({
      display: 'grid',
      gridTemplateColumns: 'repeat(7, 1fr)',
      gap: 1,
    }),
    day: css({
      border: 'none',
      background: 'transparent',
      color: theme.colors.text.primary,
      height: 26,
      fontSize: 12,
      borderRadius: theme.shape.radius.default,
      cursor: 'pointer',
      '&:hover': {
        background: theme.colors.action.hover,
      },
    }),
    outside: css({
      color: theme.colors.text.disabled,
    }),
    range: css({
      background: theme.colors.action.selected,
    }),
    edge: css({
      background: theme.colors.primary.main,
      color: theme.colors.primary.contrastText,
    }),
    hint: css({
      marginTop: theme.spacing(0.5),
      marginBottom: theme.spacing(0.5),
      color: theme.colors.text.secondary,
      fontSize: 11,
    }),
  };
}
