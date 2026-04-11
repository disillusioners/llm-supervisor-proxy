import { useState, useEffect, useMemo } from 'preact/hooks';
import { useUsage } from '../../hooks/useApi';
import { UsageSummaryCards } from './UsageSummaryCards';
import { UsageTable } from './UsageTable';
import { UsageChart } from './UsageChart';

function toHourFormat(d: Date): string {
  return d.getFullYear() + '-' +
    String(d.getMonth() + 1).padStart(2, '0') + '-' +
    String(d.getDate()).padStart(2, '0') + 'T' +
    String(d.getHours()).padStart(2, '0');
}

type DateRange = '24h' | '7d' | '30d' | 'custom';

export function UsageTab() {
  const { usageData, usageTokens, summary, loading, error, fetchUsage, fetchSummary } = useUsage();

  const [selectedTokenId, setSelectedTokenId] = useState('');
  const [dateRange, setDateRange] = useState<DateRange>('24h');
  const [customFrom, setCustomFrom] = useState('');
  const [customTo, setCustomTo] = useState('');
  const [view, setView] = useState<'hourly' | 'daily'>('hourly');
  const [displayMode, setDisplayMode] = useState<'chart' | 'table'>('chart');

  // Calculate date range boundaries
  const { from, to } = useMemo(() => {
    const now = new Date();
    const toFormatted = toHourFormat(now);

    switch (dateRange) {
      case '24h': {
        const fromDate = new Date(now.getTime() - 24 * 60 * 60 * 1000);
        return { from: toHourFormat(fromDate), to: toFormatted };
      }
      case '7d': {
        const fromDate = new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000);
        return { from: toHourFormat(fromDate), to: toFormatted };
      }
      case '30d': {
        const fromDate = new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000);
        return { from: toHourFormat(fromDate), to: toFormatted };
      }
      case 'custom': {
        const fromDate = customFrom ? new Date(customFrom) : new Date(now.getTime() - 24 * 60 * 60 * 1000);
        const toDate = customTo ? new Date(customTo) : now;
        return { from: toHourFormat(fromDate), to: toHourFormat(toDate) };
      }
    }
  }, [dateRange, customFrom, customTo]);

  // Fetch data when parameters change
  useEffect(() => {
    fetchSummary(from, to);
    fetchUsage({
      token_id: selectedTokenId || undefined,
      from,
      to,
      view,
    });
  }, [selectedTokenId, from, to, view, fetchSummary, fetchUsage]);

  const handleDateRangeChange = (range: DateRange) => {
    setDateRange(range);
    if (range === 'custom') {
      // Initialize custom dates to last 24h range (datetime-local format: YYYY-MM-DDTHH)
      const now = new Date();
      const yesterday = new Date(now.getTime() - 24 * 60 * 60 * 1000);
      setCustomTo(toHourFormat(now).slice(0, 16));
      setCustomFrom(toHourFormat(yesterday).slice(0, 16));
    }
  };

  const dateRangeButtons: { label: string; value: DateRange }[] = [
    { label: 'Last 24h', value: '24h' },
    { label: 'Last 7d', value: '7d' },
    { label: 'Last 30d', value: '30d' },
    { label: 'Custom', value: 'custom' },
  ];

  return (
    <div class="space-y-6">
      {/* Filter bar */}
      <div class="flex flex-wrap gap-4 items-end">
        {/* Token selector */}
        <div>
          <label class="block text-sm text-gray-400 mb-1">Token</label>
          <select
            value={selectedTokenId}
            onChange={(e) => setSelectedTokenId((e.target as HTMLSelectElement).value)}
            class="bg-gray-700 border border-gray-600 text-gray-100 rounded-md px-3 py-2 text-sm min-w-[140px]"
          >
            <option value="">All Tokens</option>
            {usageTokens.map((t) => (
              <option key={t.token_id} value={t.token_id}>
                {t.name}
              </option>
            ))}
          </select>
        </div>

        {/* Date range preset buttons */}
        <div>
          <label class="block text-sm text-gray-400 mb-1">Period</label>
          <div class="flex gap-2">
            {dateRangeButtons.map((btn) => (
              <button
                key={btn.value}
                onClick={() => handleDateRangeChange(btn.value)}
                class={`px-3 py-1.5 rounded-md text-sm font-medium transition-colors ${
                  dateRange === btn.value
                    ? 'bg-blue-600 text-white'
                    : 'bg-gray-700 text-gray-400 hover:text-white'
                }`}
              >
                {btn.label}
              </button>
            ))}
          </div>
        </div>

        {/* Custom date inputs */}
        {dateRange === 'custom' && (
          <div class="flex gap-2 items-end">
            <div>
              <label class="block text-sm text-gray-400 mb-1">From</label>
              <input
                type="datetime-local"
                value={customFrom}
                onChange={(e) => setCustomFrom((e.target as HTMLInputElement).value)}
                class="bg-gray-700 border border-gray-600 text-gray-100 rounded-md px-3 py-2 text-sm"
              />
            </div>
            <div>
              <label class="block text-sm text-gray-400 mb-1">To</label>
              <input
                type="datetime-local"
                value={customTo}
                onChange={(e) => setCustomTo((e.target as HTMLInputElement).value)}
                class="bg-gray-700 border border-gray-600 text-gray-100 rounded-md px-3 py-2 text-sm"
              />
            </div>
          </div>
        )}
      </div>

      {/* Error banner */}
      {error && (
        <div class="bg-red-900/50 border border-red-700 text-red-200 rounded-md p-3 text-sm">
          {error}
        </div>
      )}

      {/* Summary cards */}
      <UsageSummaryCards summary={summary} loading={loading} />

      {/* Display mode toggle and view toggle row */}
      <div class="flex gap-4 items-center">
        {/* Display mode toggle (chart/table) */}
        <div class="flex gap-1 bg-gray-800 rounded-lg p-1">
          <button
            onClick={() => setDisplayMode('chart')}
            class={`px-3 py-1.5 rounded text-sm font-medium transition-colors ${
              displayMode === 'chart'
                ? 'bg-blue-600 text-white'
                : 'text-gray-400 hover:text-white'
            }`}
          >
            📊 Chart
          </button>
          <button
            onClick={() => setDisplayMode('table')}
            class={`px-3 py-1.5 rounded text-sm font-medium transition-colors ${
              displayMode === 'table'
                ? 'bg-blue-600 text-white'
                : 'text-gray-400 hover:text-white'
            }`}
          >
            📋 Table
          </button>
        </div>

        {/* View toggle (hourly/daily) - always visible */}
        <div class="flex gap-1 bg-gray-800 rounded-lg p-1">
          <button
            onClick={() => setView('hourly')}
            class={`px-3 py-1.5 rounded text-sm font-medium transition-colors ${
              view === 'hourly'
                ? 'bg-blue-600 text-white'
                : 'text-gray-400 hover:text-white'
            }`}
          >
            Hourly
          </button>
          <button
            onClick={() => setView('daily')}
            class={`px-3 py-1.5 rounded text-sm font-medium transition-colors ${
              view === 'daily'
                ? 'bg-blue-600 text-white'
                : 'text-gray-400 hover:text-white'
            }`}
          >
            Daily
          </button>
        </div>
      </div>

      {/* Chart or Table view */}
      {displayMode === 'chart' ? (
        <UsageChart
          data={usageData?.data || []}
          view={view}
          loading={loading}
        />
      ) : (
        <UsageTable
          data={usageData?.data || []}
          totals={usageData?.totals || null}
          loading={loading}
          view={view}
          onViewChange={setView}
        />
      )}
    </div>
  );
}
