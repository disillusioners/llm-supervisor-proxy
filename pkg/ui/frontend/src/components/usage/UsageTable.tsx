import type { HourlyUsageRow, UsageTotals } from '../../types';
import { formatTokenCount, formatHourBucket } from '../../utils/helpers';

interface UsageTableProps {
  data: HourlyUsageRow[];
  totals: UsageTotals | null;
  loading: boolean;
  view: 'hourly' | 'daily';
  onViewChange: (view: 'hourly' | 'daily') => void;
}

interface BarCellProps {
  value: number;
  max: number;
  color: string;
  formattedValue: string;
}

function BarCell({ value, max, color, formattedValue }: BarCellProps) {
  const width = max > 0 ? (value / max) * 100 : 0;
  return (
    <div class="flex flex-col gap-1">
      <span class="text-gray-100">{formattedValue}</span>
      <div class="w-full bg-gray-700 h-1 rounded-full overflow-hidden">
        <div class={color + ' h-full rounded-full'} style={{ width: width + '%' }} />
      </div>
    </div>
  );
}

function SkeletonRow() {
  return (
    <tr class="border-t border-gray-700">
      <td class="px-4 py-3"><div class="h-4 bg-gray-700 rounded w-24 animate-pulse" /></td>
      <td class="px-4 py-3"><div class="h-4 bg-gray-700 rounded w-16 animate-pulse" /></td>
      <td class="px-4 py-3"><div class="h-4 bg-gray-700 rounded w-20 animate-pulse" /></td>
      <td class="px-4 py-3"><div class="h-4 bg-gray-700 rounded w-20 animate-pulse" /></td>
      <td class="px-4 py-3"><div class="h-4 bg-gray-700 rounded w-20 animate-pulse" /></td>
    </tr>
  );
}

export function UsageTable({ data, totals, loading, view, onViewChange }: UsageTableProps) {
  // Compute max values for bar chart widths
  const maxValues = data.reduce(
    (acc, row) => ({
      request_count: Math.max(acc.request_count, row.request_count),
      prompt_tokens: Math.max(acc.prompt_tokens, row.prompt_tokens),
      completion_tokens: Math.max(acc.completion_tokens, row.completion_tokens),
      total_tokens: Math.max(acc.total_tokens, row.total_tokens),
    }),
    { request_count: 0, prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 }
  );

  // Max values including totals for consistent scaling
  const maxWithTotals = totals
    ? {
        request_count: Math.max(maxValues.request_count, totals.request_count),
        prompt_tokens: Math.max(maxValues.prompt_tokens, totals.prompt_tokens),
        completion_tokens: Math.max(maxValues.completion_tokens, totals.completion_tokens),
        total_tokens: Math.max(maxValues.total_tokens, totals.total_tokens),
      }
    : maxValues;

  return (
    <div class="space-y-4">
      {/* View Toggle */}
      <div class="flex gap-2">
        <button
          onClick={() => onViewChange('hourly')}
          class={`px-4 py-2 text-sm font-medium rounded-md transition-colors ${
            view === 'hourly'
              ? 'bg-blue-600 text-white'
              : 'bg-gray-700 text-gray-400 hover:text-white'
          }`}
        >
          Hourly
        </button>
        <button
          onClick={() => onViewChange('daily')}
          class={`px-4 py-2 text-sm font-medium rounded-md transition-colors ${
            view === 'daily'
              ? 'bg-blue-600 text-white'
              : 'bg-gray-700 text-gray-400 hover:text-white'
          }`}
        >
          Daily
        </button>
      </div>

      {/* Table */}
      <div class="overflow-x-auto">
        <table class="w-full text-sm bg-gray-800/50 rounded-lg overflow-hidden">
          <thead>
            <tr class="bg-gray-700 text-gray-400 uppercase text-xs">
              <th class="px-4 py-3 text-left font-medium">{view === 'hourly' ? 'Hour' : 'Day'}</th>
              <th class="px-4 py-3 text-left font-medium">Requests</th>
              <th class="px-4 py-3 text-left font-medium">Prompt Tokens</th>
              <th class="px-4 py-3 text-left font-medium">Completion Tokens</th>
              <th class="px-4 py-3 text-left font-medium">Total Tokens</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <>
                <SkeletonRow />
                <SkeletonRow />
                <SkeletonRow />
                <SkeletonRow />
                <SkeletonRow />
              </>
            ) : data.length === 0 ? (
              <tr>
                <td colSpan={5} class="px-4 py-12 text-center text-gray-400">
                  No usage data found for the selected period
                </td>
              </tr>
            ) : (
              <>
                {data.map((row) => (
                  <tr key={row.hour_bucket} class="border-t border-gray-700 hover:bg-gray-700/50">
                    <td class="px-4 py-3 text-gray-100">{formatHourBucket(row.hour_bucket)}</td>
                    <td class="px-4 py-3">
                      <BarCell
                        value={row.request_count}
                        max={maxWithTotals.request_count}
                        color="bg-blue-500"
                        formattedValue={row.request_count.toLocaleString()}
                      />
                    </td>
                    <td class="px-4 py-3">
                      <BarCell
                        value={row.prompt_tokens}
                        max={maxWithTotals.prompt_tokens}
                        color="bg-purple-500"
                        formattedValue={formatTokenCount(row.prompt_tokens)}
                      />
                    </td>
                    <td class="px-4 py-3">
                      <BarCell
                        value={row.completion_tokens}
                        max={maxWithTotals.completion_tokens}
                        color="bg-green-500"
                        formattedValue={formatTokenCount(row.completion_tokens)}
                      />
                    </td>
                    <td class="px-4 py-3">
                      <BarCell
                        value={row.total_tokens}
                        max={maxWithTotals.total_tokens}
                        color="bg-yellow-500"
                        formattedValue={formatTokenCount(row.total_tokens)}
                      />
                    </td>
                  </tr>
                ))}
                {totals && (
                  <tr class="border-t border-gray-600 bg-gray-700 font-bold">
                    <td class="px-4 py-3 text-gray-100">Total</td>
                    <td class="px-4 py-3">
                      <BarCell
                        value={totals.request_count}
                        max={maxWithTotals.request_count}
                        color="bg-blue-500"
                        formattedValue={totals.request_count.toLocaleString()}
                      />
                    </td>
                    <td class="px-4 py-3">
                      <BarCell
                        value={totals.prompt_tokens}
                        max={maxWithTotals.prompt_tokens}
                        color="bg-purple-500"
                        formattedValue={formatTokenCount(totals.prompt_tokens)}
                      />
                    </td>
                    <td class="px-4 py-3">
                      <BarCell
                        value={totals.completion_tokens}
                        max={maxWithTotals.completion_tokens}
                        color="bg-green-500"
                        formattedValue={formatTokenCount(totals.completion_tokens)}
                      />
                    </td>
                    <td class="px-4 py-3">
                      <BarCell
                        value={totals.total_tokens}
                        max={maxWithTotals.total_tokens}
                        color="bg-yellow-500"
                        formattedValue={formatTokenCount(totals.total_tokens)}
                      />
                    </td>
                  </tr>
                )}
              </>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
