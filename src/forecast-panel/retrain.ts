const pending = new Set<string>();
let allGen = 0;
const seenAll = new Map<string, number>();

/**
 * Where a queued retrain applies. Panel ids are only unique within a dashboard, so the
 * dashboard uid is part of the key: two dashboards (or a dashboard and a panel view)
 * whose panel ids collide must not share one slot.
 */
export function retrainKey(dashboardUid: string | null | undefined, panelId: number): string {
  return `${dashboardUid ?? ''}#${panelId}`;
}

export function queueRetrain(key: string): void {
  pending.add(key);
}

/** Drop a queued retrain nobody will run. */
export function clearRetrain(key: string): void {
  pending.delete(key);
}

export function queueRetrainAll(): void {
  allGen += 1;
}

export function takeRetrain(key: string): boolean {
  if (pending.delete(key)) {
    return true;
  }
  const seen = seenAll.get(key) ?? 0;
  if (allGen > seen) {
    seenAll.set(key, allGen);
    return true;
  }
  return false;
}
