import { OverlayLoadGate, clampMaxInflightLoads } from './overlayInflight';

describe('clampMaxInflightLoads', () => {
  it.each([
    [undefined, 1],
    [null, 1],
    [0, 1],
    [-3, 1],
    [1.8, 1],
    [3, 3],
    ['2', 2],
    ['nope', 1],
  ])('%j → %s', (input, want) => {
    expect(clampMaxInflightLoads(input)).toBe(want);
  });
});

describe('OverlayLoadGate', () => {
  it('aborts the oldest load when the cap is full', () => {
    const gate = new OverlayLoadGate();
    const first = gate.start(1);
    const second = gate.start(1);
    expect(first.signal.aborted).toBe(true);
    expect(second.signal.aborted).toBe(false);
    gate.finish(second);
    const third = gate.start(1);
    expect(second.signal.aborted).toBe(false);
    expect(third.signal.aborted).toBe(false);
  });

  it('keeps two loads when the cap is 2', () => {
    const gate = new OverlayLoadGate();
    const a = gate.start(2);
    const b = gate.start(2);
    expect(a.signal.aborted).toBe(false);
    expect(b.signal.aborted).toBe(false);
    const c = gate.start(2);
    expect(a.signal.aborted).toBe(true);
    expect(b.signal.aborted).toBe(false);
    expect(c.signal.aborted).toBe(false);
  });
});
