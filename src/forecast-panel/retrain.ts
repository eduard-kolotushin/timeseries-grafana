const pending = new Set<number>();
let allGen = 0;
const seenAll = new Map<number, number>();

export function queueRetrain(panelId: number): void {
  pending.add(panelId);
}

export function queueRetrainAll(): void {
  allGen += 1;
}

export function takeRetrain(panelId: number): boolean {
  if (pending.delete(panelId)) {
    return true;
  }
  const seen = seenAll.get(panelId) ?? 0;
  if (allGen > seen) {
    seenAll.set(panelId, allGen);
    return true;
  }
  return false;
}
