import { strict as assert } from "node:assert";
import test from "node:test";

import { median, worst, type Sample } from "./latency.ts";

const samples: Sample[] = [
  { route: "/search", ms: 9 },
  { route: "/search", ms: 120 },
  { route: "/search", ms: 11 },
  { route: "/health", ms: 2 },
  { route: "/search", ms: 1000 },
  { route: "/search", ms: 7 },
];

test("median is the middle latency, not the middle string", () => {
  assert.equal(median(samples, "/search"), 11);
  assert.equal(median(samples, "/health"), 2);
  assert.equal(median([], "/search"), 0);
});

test("an even count averages the middle two", () => {
  assert.equal(
    median(
      [
        { route: "/a", ms: 3 },
        { route: "/a", ms: 100 },
        { route: "/a", ms: 5 },
        { route: "/a", ms: 9 },
      ],
      "/a",
    ),
    7,
  );
});

test("worst is the slowest recorded", () => {
  assert.equal(worst(samples, "/search"), 1000);
  assert.equal(worst(samples, "/nothing"), 0);
});
