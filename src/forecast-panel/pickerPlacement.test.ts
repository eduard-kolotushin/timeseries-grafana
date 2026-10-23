import { CALENDAR_DOCK_WIDTH, PICKER_MARGIN, PickerLayout, placePicker, pickerLayout } from './pickerPlacement';

describe('pickerLayout', () => {
  it.each<[string, number, number, number, PickerLayout]>([
    ['docks on a desktop with room for the calendar', 1127, 1440, 900, 'wide'],
    ['stacks on a tablet where the dock would be off-screen', 455, 768, 900, 'stacked'],
    ['stacks when the trigger is too close to the left edge', 695, 1024, 900, 'stacked'],
    ['docks at the exact threshold, with an 8px margin', 700, 1024, 900, 'wide'],
    ['stacks on a viewport too short for the docked picker', 1127, 1440, 300, 'stacked'],
  ])('%s', (_name, triggerLeft, viewportWidth, viewportHeight, want) => {
    expect(pickerLayout(triggerLeft, viewportWidth, viewportHeight)).toBe(want);
  });
});

describe('placePicker', () => {
  it('keeps the docked calendar inside the viewport', () => {
    const got = placePicker({
      layout: 'wide',
      trigger: { left: 1127, top: 514, bottom: 546 },
      picker: { width: 440, height: 214 },
      viewport: { width: 1440, height: 900 },
    });
    expect(got).toEqual({ left: 683, top: 296 });
    expect(got.left - CALENDAR_DOCK_WIDTH).toBeGreaterThanOrEqual(PICKER_MARGIN);
  });

  it('flips below a trigger near the top instead of clipping above', () => {
    const got = placePicker({
      layout: 'wide',
      trigger: { left: 1127, top: 20, bottom: 52 },
      picker: { width: 440, height: 214 },
      viewport: { width: 1440, height: 900 },
    });
    expect(got.top).toBe(56);
  });

  it('clamps a stacked picker horizontally and vertically', () => {
    const got = placePicker({
      layout: 'stacked',
      trigger: { left: 455, top: 514, bottom: 546 },
      picker: { width: 360, height: 700 },
      viewport: { width: 768, height: 900 },
    });
    expect(got).toEqual({ left: 400, top: 192 });
  });

  it('pins a picker taller than the viewport to the top margin', () => {
    const got = placePicker({
      layout: 'stacked',
      trigger: { left: 100, top: 400, bottom: 432 },
      picker: { width: 360, height: 1000 },
      viewport: { width: 768, height: 900 },
    });
    expect(got).toEqual({ left: 100, top: PICKER_MARGIN });
  });
});
