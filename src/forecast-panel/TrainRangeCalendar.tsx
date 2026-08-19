import React, { useMemo, useState } from 'react';
import { css, cx } from '@emotion/css';
import { GrafanaTheme2 } from '@grafana/data';
import { Button, IconButton, useStyles2 } from '@grafana/ui';

const WEEKDAYS = ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su'];

function startOfDay(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate());
}

function addMonths(d: Date, n: number): Date {
  return new Date(d.getFullYear(), d.getMonth() + n, 1);
}

function isSameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

function between(d: Date, a: Date, b: Date): boolean {
  const t = startOfDay(d).getTime();
  const x = startOfDay(a).getTime();
  const y = startOfDay(b).getTime();
  return t >= Math.min(x, y) && t <= Math.max(x, y);
}

function monthCells(cursor: Date): Date[] {
  const first = new Date(cursor.getFullYear(), cursor.getMonth(), 1);
  const lead = (first.getDay() + 6) % 7;
  const start = new Date(first);
  start.setDate(1 - lead);
  const cells: Date[] = [];
  for (let i = 0; i < 42; i++) {
    cells.push(new Date(start.getFullYear(), start.getMonth(), start.getDate() + i));
  }
  return cells;
}

export function TrainRangeCalendar({
  from,
  to,
  onSelect,
}: {
  from: Date;
  to: Date;
  onSelect: (from: Date, to: Date) => void;
}) {
  const styles = useStyles2(getStyles);
  const [cursor, setCursor] = useState(() => new Date(from.getFullYear(), from.getMonth(), 1));
  const [picking, setPicking] = useState<Date | null>(null);
  const cells = useMemo(() => monthCells(cursor), [cursor]);
  const title = cursor.toLocaleString(undefined, { month: 'long', year: 'numeric' });

  const onDay = (d: Date) => {
    const day = startOfDay(d);
    if (!picking) {
      setPicking(day);
      return;
    }
    const a = picking.getTime() <= day.getTime() ? picking : day;
    const b = picking.getTime() <= day.getTime() ? day : picking;
    onSelect(a, b);
    setPicking(null);
  };

  return (
    <div className={styles.wrap}>
      <div className={styles.header}>
        <IconButton name="angle-left" tooltip="Previous month" onClick={() => setCursor((c) => addMonths(c, -1))} />
        <div className={styles.title}>{title}</div>
        <IconButton name="angle-right" tooltip="Next month" onClick={() => setCursor((c) => addMonths(c, 1))} />
      </div>
      <div className={styles.weekdays}>
        {WEEKDAYS.map((d) => (
          <div key={d}>{d}</div>
        ))}
      </div>
      <div className={styles.grid}>
        {cells.map((d) => {
          const inMonth = d.getMonth() === cursor.getMonth();
          const selected = picking
            ? isSameDay(d, picking)
            : isSameDay(d, from) || isSameDay(d, to) || between(d, from, to);
          const edge = picking ? isSameDay(d, picking) : isSameDay(d, from) || isSameDay(d, to);
          return (
            <button
              key={d.toISOString()}
              type="button"
              className={cx(styles.day, !inMonth && styles.outside, selected && styles.range, edge && styles.edge)}
              onClick={() => onDay(d)}
            >
              {d.getDate()}
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
      marginTop: theme.spacing(1),
      marginBottom: theme.spacing(1),
      padding: theme.spacing(1),
      border: `1px solid ${theme.colors.border.weak}`,
      borderRadius: theme.shape.radius.default,
      background: theme.colors.background.primary,
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
