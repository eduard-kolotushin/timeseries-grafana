import { queueRetrain, queueRetrainAll, takeRetrain } from './retrain';

describe('retrain queue', () => {
  it('is one-shot per panel id', () => {
    queueRetrain(7);
    expect(takeRetrain(3)).toBe(false);
    expect(takeRetrain(7)).toBe(true);
    expect(takeRetrain(7)).toBe(false);
  });

  it('retrain-all is seen once per panel per generation', () => {
    queueRetrainAll();
    expect(takeRetrain(1)).toBe(true);
    expect(takeRetrain(2)).toBe(true);
    expect(takeRetrain(1)).toBe(false);
    queueRetrainAll();
    expect(takeRetrain(1)).toBe(true);
  });
});
