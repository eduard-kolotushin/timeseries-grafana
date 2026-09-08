import { monthCells } from './TrainRangeCalendar';

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
