/**
 * Placement for the range popover. The calendar is docked to the left of the
 * popover, so the popover can only use the docked layout when the trigger sits
 * far enough from the left edge for both boxes. Otherwise the calendar renders
 * inside the popover and the whole box is clamped to the viewport.
 */
export const PICKER_MARGIN = 8;
export const PICKER_GAP = 4;
export const PICKER_WIDE_WIDTH = 440;
export const PICKER_STACKED_WIDTH = 360;
export const CALENDAR_DOCK_WIDTH = 248;
export const PICKER_MIN_VIEWPORT_WIDTH = 760;
export const PICKER_MIN_VIEWPORT_HEIGHT = 360;

export type PickerLayout = 'wide' | 'stacked';

/** The docked calendar needs room to the left of the popover, not only inside the viewport. */
export function pickerLayout(triggerLeft: number, viewportWidth: number, viewportHeight: number): PickerLayout {
  const required = PICKER_WIDE_WIDTH + CALENDAR_DOCK_WIDTH + PICKER_GAP + PICKER_MARGIN;
  return viewportWidth >= PICKER_MIN_VIEWPORT_WIDTH &&
    viewportHeight >= PICKER_MIN_VIEWPORT_HEIGHT &&
    triggerLeft >= required
    ? 'wide'
    : 'stacked';
}

export function clampPickerLeft(left: number, width: number, viewportWidth: number): number {
  const max = Math.max(PICKER_MARGIN, viewportWidth - width - PICKER_MARGIN);
  return Math.min(Math.max(left, PICKER_MARGIN), max);
}

export function clampPickerTop(top: number, height: number, viewportHeight: number): number {
  const max = Math.max(PICKER_MARGIN, viewportHeight - height - PICKER_MARGIN);
  return Math.min(Math.max(top, PICKER_MARGIN), max);
}

export type PlacementInput = {
  layout: PickerLayout;
  trigger: { left: number; top: number; bottom: number };
  picker: { width: number; height: number };
  viewport: { width: number; height: number };
};

/** Place above the trigger when possible, below when the top would clip, and clamp to the viewport. */
export function placePicker({ layout, trigger, picker, viewport }: PlacementInput): { left: number; top: number } {
  const left = layout === 'wide' ? trigger.left - picker.width - PICKER_GAP : trigger.left;
  let top = trigger.top - picker.height - PICKER_GAP;
  if (top < PICKER_MARGIN) {
    top = trigger.bottom + PICKER_GAP;
  }
  return {
    left: clampPickerLeft(left, picker.width, viewport.width),
    top: clampPickerTop(top, picker.height, viewport.height),
  };
}
