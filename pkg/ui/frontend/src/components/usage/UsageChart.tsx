import { useRef, useEffect } from 'preact/hooks';
import { Chart, LineController, LineElement, PointElement, LinearScale, CategoryScale, Title, Legend, Filler } from 'chart.js';
import type { UsageDataRow } from '../../types';
import { formatHourBucket } from '../../utils/helpers';

// Register Chart.js components
Chart.register(LineController, LineElement, PointElement, LinearScale, CategoryScale, Title, Legend, Filler);

// Color palette for token lines
const COLORS = [
  '#3b82f6', // blue
  '#10b981', // emerald
  '#f59e0b', // amber
  '#ef4444', // red
  '#8b5cf6', // violet
  '#ec4899', // pink
  '#06b6d4', // cyan
  '#84cc16', // lime
];

interface UsageChartProps {
  data: UsageDataRow[];
  view: 'hourly' | 'daily';
  loading: boolean;
}

export function UsageChart({ data, view, loading }: UsageChartProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const chartRef = useRef<Chart | null>(null);

  useEffect(() => {
    // Destroy existing chart
    if (chartRef.current) {
      chartRef.current.destroy();
      chartRef.current = null;
    }

    // Don't create chart if no canvas, no data, or loading
    if (!canvasRef.current || data.length === 0 || loading) {
      return;
    }

    const ctx = canvasRef.current.getContext('2d');
    if (!ctx) return;

    // Group data by bucket and token
    const buckets = [...new Set(data.map(d => d.hour_bucket))].sort();
    const tokenIds = [...new Set(data.map(d => d.token_id))];

    // Create datasets: one per token + one total line
    const datasets: { label: string; data: number[]; borderColor: string; backgroundColor: string; borderWidth: number; borderDash?: number[]; tension: number; pointRadius: number; }[] = [];

    // Add a line for each unique token
    tokenIds.forEach((tokenId, index) => {
      const tokenData = data.find(d => d.token_id === tokenId);
      const color = COLORS[index % COLORS.length];
      datasets.push({
        label: tokenData?.token_name || tokenId,
        data: buckets.map(bucket => {
          const row = data.find(d => d.hour_bucket === bucket && d.token_id === tokenId);
          return row?.total_tokens || 0;
        }),
        borderColor: color,
        backgroundColor: color + '20',
        borderWidth: 2,
        tension: 0.3,
        pointRadius: view === 'hourly' ? 0 : 3,
      });
    });

    // Add Total line (sum of all tokens per bucket)
    datasets.push({
      label: 'Total',
      data: buckets.map(bucket => {
        return data
          .filter(d => d.hour_bucket === bucket)
          .reduce((sum, d) => sum + d.total_tokens, 0);
      }),
      borderColor: '#ffffff',
      backgroundColor: '#ffffff10',
      borderWidth: 3,
      borderDash: [5, 5],
      tension: 0.3,
      pointRadius: view === 'hourly' ? 0 : 3,
    });

    // Create labels
    const labels = buckets.map(formatHourBucket);

    // Create chart
    chartRef.current = new Chart(ctx, {
      type: 'line',
      data: {
        labels,
        datasets,
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        interaction: {
          mode: 'index',
          intersect: false,
        },
        plugins: {
          legend: {
            position: 'bottom',
            labels: {
              color: '#9ca3af',
              padding: 16,
              usePointStyle: true,
              font: {
                size: 12,
              },
            },
          },
          title: {
            display: false,
          },
          tooltip: {
            backgroundColor: '#1f2937',
            titleColor: '#f3f4f6',
            bodyColor: '#d1d5db',
            borderColor: '#374151',
            borderWidth: 1,
            padding: 12,
            callbacks: {
              label: (context) => {
                const value = context.parsed.y;
                return `${context.dataset.label}: ${value.toLocaleString()} tokens`;
              },
            },
          },
        },
        scales: {
          x: {
            grid: {
              color: '#374151',
            },
            ticks: {
              color: '#9ca3af',
              maxRotation: 45,
              minRotation: 0,
              autoSkip: buckets.length > 12,
              maxTicksLimit: 12,
            },
          },
          y: {
            grid: {
              color: '#374151',
            },
            ticks: {
              color: '#9ca3af',
              callback: (value) => {
                if (typeof value === 'number') {
                  if (value >= 1000000) return (value / 1000000).toFixed(1) + 'M';
                  if (value >= 1000) return (value / 1000).toFixed(1) + 'K';
                  return value.toString();
                }
                return value;
              },
            },
            beginAtZero: true,
          },
        },
      },
    });

    return () => {
      if (chartRef.current) {
        chartRef.current.destroy();
        chartRef.current = null;
      }
    };
  }, [data, view, loading]);

  if (loading) {
    return (
      <div class="h-80 flex items-center justify-center bg-gray-800/50 rounded-lg">
        <div class="animate-pulse">
          <div class="h-64 w-full bg-gray-700 rounded" style={{ width: '600px' }} />
        </div>
      </div>
    );
  }

  if (data.length === 0) {
    return (
      <div class="h-80 flex items-center justify-center bg-gray-800/50 rounded-lg">
        <p class="text-gray-400 text-sm">No data available for the selected period</p>
      </div>
    );
  }

  return (
    <div class="h-80 bg-gray-800/50 rounded-lg p-4">
      <canvas ref={canvasRef} />
    </div>
  );
}
