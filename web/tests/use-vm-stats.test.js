import test from 'node:test';
import assert from 'node:assert/strict';

const { appendSample, sampleTime, buildChartData } = await import('../src/hooks/vmStatsHelpers.js');

test('sampleTime parses ISO timestamps and rejects invalid values', () => {
  assert.equal(sampleTime({ timestamp: '2026-05-01T00:00:00.000Z' }), Date.parse('2026-05-01T00:00:00.000Z'));
  assert.equal(sampleTime({ timestamp: 'not-a-date' }), null);
  assert.equal(sampleTime({}), null);
  assert.equal(sampleTime(null), null);
});

test('appendSample appends new samples in order', () => {
  const a = { timestamp: '2026-05-01T00:00:00.000Z', cpu_percent: 10 };
  const b = { timestamp: '2026-05-01T00:00:10.000Z', cpu_percent: 20 };
  let history = [];
  history = appendSample(history, a, 5);
  history = appendSample(history, b, 5);
  assert.equal(history.length, 2);
  assert.equal(history[0], a);
  assert.equal(history[1], b);
});

test('appendSample rejects out-of-order or duplicate samples', () => {
  const a = { timestamp: '2026-05-01T00:00:10.000Z', cpu_percent: 10 };
  const b = { timestamp: '2026-05-01T00:00:00.000Z', cpu_percent: 20 };
  const dup = { timestamp: '2026-05-01T00:00:10.000Z', cpu_percent: 99 };
  let history = appendSample([], a, 5);
  history = appendSample(history, b, 5);
  history = appendSample(history, dup, 5);
  assert.equal(history.length, 1);
  assert.equal(history[0], a);
});

test('appendSample respects the cap by dropping the oldest sample', () => {
  let history = [];
  for (let i = 0; i < 5; i += 1) {
    const ts = new Date(Date.UTC(2026, 4, 1, 0, 0, i)).toISOString();
    history = appendSample(history, { timestamp: ts, cpu_percent: i }, 3);
  }
  assert.equal(history.length, 3);
  assert.equal(history[0].cpu_percent, 2);
  assert.equal(history[2].cpu_percent, 4);
});

test('appendSample skips samples without a parseable timestamp', () => {
  const a = { timestamp: '2026-05-01T00:00:00.000Z', cpu_percent: 10 };
  let history = appendSample([], a, 5);
  history = appendSample(history, { cpu_percent: 999 }, 5);
  history = appendSample(history, { timestamp: '???', cpu_percent: 999 }, 5);
  assert.equal(history.length, 1);
  assert.equal(history[0].cpu_percent, 10);
});

test('buildChartData produces uPlot columnar layout with null gaps', () => {
  const history = [
    { timestamp: '2026-05-01T00:00:00.000Z', cpu_percent: 5, mem_used_mb: 100 },
    { timestamp: '2026-05-01T00:00:10.000Z', cpu_percent: 6 }, // mem missing
    { timestamp: 'invalid' },                                  // dropped
    { timestamp: '2026-05-01T00:00:20.000Z', cpu_percent: 7, mem_used_mb: 110 },
  ];
  const [xs, cpu, mem] = buildChartData(history, ['cpu_percent', 'mem_used_mb']);
  assert.deepEqual(xs, [
    Date.parse('2026-05-01T00:00:00.000Z') / 1000,
    Date.parse('2026-05-01T00:00:10.000Z') / 1000,
    Date.parse('2026-05-01T00:00:20.000Z') / 1000,
  ]);
  assert.deepEqual(cpu, [5, 6, 7]);
  assert.deepEqual(mem, [100, null, 110]);
});

test('buildChartData returns empty rails for empty history', () => {
  const [xs, cpu] = buildChartData([], ['cpu_percent']);
  assert.deepEqual(xs, []);
  assert.deepEqual(cpu, []);
});
