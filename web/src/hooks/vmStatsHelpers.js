// Shared helpers for the VM-stats SSE hook. Extracted into their own module
// (with no React or API-client imports) so they remain testable under
// `node --test` without a full DOM/React shim.

export function sampleTime(sample) {
  if (!sample?.timestamp) return null;
  const ms = Date.parse(sample.timestamp);
  return Number.isFinite(ms) ? ms : null;
}

export function appendSample(history, sample, cap) {
  if (!sample) return history;
  const ts = sampleTime(sample);
  if (ts == null) return history;
  const last = history.length > 0 ? sampleTime(history[history.length - 1]) : null;
  if (last != null && ts <= last) return history;
  const next = history.length >= cap ? history.slice(history.length - cap + 1) : history.slice();
  next.push(sample);
  return next;
}

// buildChartData converts a history array into uPlot's columnar layout:
// [unixSeconds[], series1[], series2[], ...]. Missing values map to null so
// uPlot draws a gap instead of guessing a baseline.
export function buildChartData(history, fields) {
  const xs = [];
  const ys = fields.map(() => []);
  for (const sample of history) {
    if (!sample?.timestamp) continue;
    const ms = Date.parse(sample.timestamp);
    if (!Number.isFinite(ms)) continue;
    xs.push(Math.floor(ms / 1000));
    fields.forEach((f, i) => {
      const v = sample[f];
      ys[i].push(typeof v === 'number' ? v : null);
    });
  }
  return [xs, ...ys];
}
