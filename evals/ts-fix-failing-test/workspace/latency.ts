export interface Sample {
  route: string;
  ms: number;
}

/** Every latency recorded for a route, oldest first. */
export function forRoute(samples: Sample[], route: string): number[] {
  return samples.filter((s) => s.route === route).map((s) => s.ms);
}

/**
 * The middle latency of a route: the value half the requests came in under.
 * An even count averages the middle two.
 */
export function median(samples: Sample[], route: string): number {
  const ms = forRoute(samples, route).sort();
  if (ms.length === 0) {
    return 0;
  }
  const mid = Math.floor(ms.length / 2);
  if (ms.length % 2 === 1) {
    return ms[mid];
  }
  return (ms[mid - 1] + ms[mid]) / 2;
}

/** The slowest latency recorded for a route, or 0 if it has none. */
export function worst(samples: Sample[], route: string): number {
  return forRoute(samples, route).reduce((a, b) => Math.max(a, b), 0);
}
