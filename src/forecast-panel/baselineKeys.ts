import { PanelTarget } from './alertFromPanel';
import { cacheKey } from './cacheKey';
import { metricTargets } from './mixed';
import { ForecastOptions } from './types';

export type BaselineKey = { name: string; key: string };

/**
 * The overlay's cache key for each series: metric targets, the panel's fit options, the
 * series display name and the visible range — exactly the call the panel makes before it
 * probes or fits. Shared by the Baseline keys option and anyone who needs the same ids.
 */
export async function baselineKeys(input: {
  targets: PanelTarget[];
  names: string[];
  options: ForecastOptions;
  visibleFromMs?: number;
  visibleToMs?: number;
}): Promise<BaselineKey[]> {
  const targets = metricTargets(input.targets);
  return Promise.all(
    input.names.map(async (name) => ({
      name,
      key: await cacheKey({
        targets,
        visibleFromMs: input.visibleFromMs,
        visibleToMs: input.visibleToMs,
        options: input.options,
        seriesName: name,
      }),
    }))
  );
}
