import { clearRetrain, queueRetrain, queueRetrainAll, retrainKey, takeRetrain } from './retrain';

describe('retrainKey', () => {
  it.each([
    ['another dashboard', retrainKey('dash-a', 1), retrainKey('dash-b', 1)],
    ['another panel of the same dashboard', retrainKey('dash-a', 1), retrainKey('dash-a', 2)],
    ['a panel view', retrainKey('dash-a', 1), retrainKey(undefined, 1)],
  ])('keys a panel apart from %s', (_name, key, other) => {
    expect(key).not.toBe(other);
  });
});

describe('retrain queue', () => {
  it.each([
    ['the panel it was queued for', 'dash-a', 7, true],
    ['another dashboard with the same panel id', 'dash-b', 7, false],
    ['another panel of the same dashboard', 'dash-a', 8, false],
    ['a panel view with the same panel id', undefined, 7, false],
  ] as const)('is taken by %s: %s', (_name, takeUid, takePanelId, want) => {
    const queued = retrainKey('dash-a', 7);
    queueRetrain(queued);
    expect(takeRetrain(retrainKey(takeUid, takePanelId))).toBe(want);
    clearRetrain(queued);
  });

  it('is one-shot for the panel that took it', () => {
    const key = retrainKey('dash-a', 7);
    queueRetrain(key);
    expect(takeRetrain(key)).toBe(true);
    expect(takeRetrain(key)).toBe(false);
  });

  it('drops a queued retrain nobody will take', () => {
    const key = retrainKey('dash-a', 9);
    queueRetrain(key);
    clearRetrain(key);
    expect(takeRetrain(key)).toBe(false);
  });

  it('retrain-all is seen once per key per generation', () => {
    queueRetrainAll();
    expect(takeRetrain(retrainKey('dash-a', 1))).toBe(true);
    expect(takeRetrain(retrainKey('dash-a', 2))).toBe(true);
    expect(takeRetrain(retrainKey('dash-a', 1))).toBe(false);
    queueRetrainAll();
    expect(takeRetrain(retrainKey('dash-a', 1))).toBe(true);
  });
});
