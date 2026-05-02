import { useEffect, useRef } from 'react';
import uPlot from 'uplot';
import 'uplot/dist/uPlot.min.css';

/**
 * MetricChart renders one or more time-series (drawn as filled line areas)
 * inside a fixed-height container using uPlot. uPlot owns a real <canvas>
 * element under the hood; we manage its lifecycle through React refs and
 * tear it down on unmount or when the dataset shape changes.
 *
 * Props:
 *   - title:    string label rendered above the chart
 *   - series:   [{ label, color, format }] — describes each y-axis series
 *   - data:     [timestamps, ...y-arrays] in uPlot's columnar format
 *   - height:   chart canvas height in pixels (default 140)
 *   - empty:    optional placeholder text rendered when no data is provided
 */
export default function MetricChart({ title, series, data, height = 140, empty = 'No samples yet' }) {
  const containerRef = useRef(null);
  const plotRef = useRef(null);

  const hasData = Array.isArray(data) && data.length > 0 && Array.isArray(data[0]) && data[0].length > 1;

  // (re-)create the uPlot instance whenever the series/title definitions
  // change. data updates use plot.setData() in a separate effect for cheaper
  // updates without a full re-render.
  useEffect(() => {
    if (!hasData) return undefined;
    const el = containerRef.current;
    if (!el) return undefined;

    const opts = {
      width: el.clientWidth || 320,
      height,
      legend: { show: false },
      cursor: { drag: { x: false, y: false } },
      scales: { x: { time: true } },
      axes: [
        {
          stroke: '#94a3b8',
          grid: { stroke: '#1f2937', width: 1 },
          ticks: { stroke: '#1f2937' },
        },
        {
          stroke: '#94a3b8',
          grid: { stroke: '#1f2937', width: 1 },
          ticks: { stroke: '#1f2937' },
          values: (_u, ticks) => ticks.map((v) => formatAxisValue(v, series[0]?.format)),
        },
      ],
      series: [
        {},
        ...series.map((s) => ({
          label: s.label,
          stroke: s.color || '#38bdf8',
          width: 1.5,
          fill: s.fill ?? `${s.color || '#38bdf8'}22`,
          points: { show: false },
        })),
      ],
    };

    const plot = new uPlot(opts, data, el);
    plotRef.current = plot;

    const ro = typeof ResizeObserver !== 'undefined' ? new ResizeObserver((entries) => {
      for (const entry of entries) {
        const w = Math.floor(entry.contentRect.width);
        if (w > 0 && plotRef.current) {
          plotRef.current.setSize({ width: w, height });
        }
      }
    }) : null;
    if (ro) ro.observe(el);

    return () => {
      if (ro) ro.disconnect();
      plot.destroy();
      plotRef.current = null;
    };
  // We intentionally exclude `data` so that data-only updates stay cheap.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasData, height, series.length, title]);

  // cheap data update path — does not destroy the uPlot instance
  useEffect(() => {
    if (!hasData) return;
    const plot = plotRef.current;
    if (plot) plot.setData(data);
  }, [data, hasData]);

  return (
    <div className="card overflow-hidden" data-testid={`metric-chart-${title?.toLowerCase().replace(/\s+/g, '-')}`}>
      <div className="flex items-center justify-between px-4 py-2 border-b border-steel-800/40">
        <h3 className="text-xs font-display font-semibold text-steel-300 tracking-wide">{title}</h3>
        <div className="flex items-center gap-3 text-[10px] font-mono uppercase tracking-[0.15em] text-steel-500">
          {series.map((s) => (
            <span key={s.label} className="flex items-center gap-1.5" data-testid={`metric-chart-legend-${s.label.toLowerCase().replace(/\s+/g, '-')}`}>
              <span className="inline-block w-2 h-2 rounded-sm" style={{ background: s.color || '#38bdf8' }} />
              {s.label}
            </span>
          ))}
        </div>
      </div>
      <div ref={containerRef} className="px-1 py-2" style={{ minHeight: height }} data-testid={`metric-chart-canvas-${title?.toLowerCase().replace(/\s+/g, '-')}`}>
        {!hasData && (
          <div className="flex items-center justify-center text-xs text-steel-500" style={{ height }}>
            {empty}
          </div>
        )}
      </div>
    </div>
  );
}

function formatAxisValue(value, format) {
  if (typeof value !== 'number') return value;
  switch (format) {
    case 'percent':
      return `${value.toFixed(0)}%`;
    case 'mb':
      return `${Math.round(value).toLocaleString()} MB`;
    case 'bps':
      return formatBps(value);
    default:
      return value.toLocaleString();
  }
}

export function formatBps(v) {
  if (typeof v !== 'number' || !Number.isFinite(v)) return '0';
  if (v >= 1 << 30) return `${(v / (1 << 30)).toFixed(1)} GB/s`;
  if (v >= 1 << 20) return `${(v / (1 << 20)).toFixed(1)} MB/s`;
  if (v >= 1 << 10) return `${(v / (1 << 10)).toFixed(1)} KB/s`;
  return `${Math.round(v)} B/s`;
}
