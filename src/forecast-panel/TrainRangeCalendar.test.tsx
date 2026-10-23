import React from 'react';
import { render, screen } from '@testing-library/react';
import { TrainRangeCalendar, monthCells } from './TrainRangeCalendar';

describe('monthCells', () => {
  it('starts Monday-first and includes 42 civil days', () => {
    // 1 Sep 2026 is a Tuesday, so Monday 31 Aug leads.
    const cells = monthCells(2026, 9);
    expect(cells).toHaveLength(42);
    expect(cells[0]).toEqual({ year: 2026, month: 8, day: 31 });
    expect(cells[1]).toEqual({ year: 2026, month: 9, day: 1 });
    expect(cells[30]).toEqual({ year: 2026, month: 9, day: 30 });
  });
});

describe('TrainRangeCalendar accessibility', () => {
  it('names every day and exposes the selected range to assistive technology', () => {
    render(
      <TrainRangeCalendar
        from={{ year: 2026, month: 9, day: 2 }}
        to={{ year: 2026, month: 9, day: 5 }}
        timeZone="UTC"
        onSelect={jest.fn()}
      />
    );

    expect(screen.getByRole('table', { name: 'September 2026 calendar' })).toBeInTheDocument();
    expect(screen.getAllByRole('columnheader')).toHaveLength(7);
    expect(screen.getByRole('button', { name: 'September 2, 2026' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: 'September 5, 2026' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: 'August 31, 2026' })).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByRole('button', { name: 'September 20, 2026' })).toHaveAttribute('aria-pressed', 'false');
  });
});
