import type { UsageSummary } from '../../types';
import { formatTokenCount } from '../../utils/helpers';

interface UsageSummaryCardsProps {
  summary: UsageSummary | null;
  loading: boolean;
}

export function UsageSummaryCards({ summary, loading }: UsageSummaryCardsProps) {
  if (loading) {
    return (
      <div class="grid grid-cols-2 lg:grid-cols-4 gap-4">
        {[1, 2, 3, 4].map((i) => (
          <div key={i} class="bg-gray-700/80 rounded-md p-4 border border-gray-600/50">
            <div class="animate-pulse">
              <div class="h-4 bg-gray-600 rounded w-24 mb-2" />
              <div class="h-8 bg-gray-600 rounded w-16 mt-3" />
            </div>
          </div>
        ))}
      </div>
    );
  }

  const totalRequests = summary?.grand_total.total_requests ?? '--';
  const promptTokens = summary?.grand_total.total_prompt_tokens;
  const completionTokens = summary?.grand_total.total_completion_tokens;
  const totalTokens = summary?.grand_total.total_tokens;

  return (
    <div class="grid grid-cols-2 lg:grid-cols-4 gap-4">
      {/* Total Requests */}
      <div class="bg-gray-700/80 rounded-md p-4 border border-gray-600/50">
        <p class="text-gray-400 text-sm">Total Requests</p>
        <p class="text-2xl font-bold text-gray-100 mt-1">
          {typeof totalRequests === 'number' ? totalRequests.toLocaleString() : totalRequests}
        </p>
      </div>

      {/* Prompt Tokens */}
      <div class="bg-gray-700/80 rounded-md p-4 border border-gray-600/50">
        <p class="text-gray-400 text-sm">Prompt Tokens</p>
        <p class="text-2xl font-bold text-gray-100 mt-1">
          {promptTokens !== undefined ? formatTokenCount(promptTokens) : '--'}
        </p>
      </div>

      {/* Completion Tokens */}
      <div class="bg-gray-700/80 rounded-md p-4 border border-gray-600/50">
        <p class="text-gray-400 text-sm">Completion Tokens</p>
        <p class="text-2xl font-bold text-gray-100 mt-1">
          {completionTokens !== undefined ? formatTokenCount(completionTokens) : '--'}
        </p>
      </div>

      {/* Total Tokens */}
      <div class="bg-gray-700/80 rounded-md p-4 border border-gray-600/50">
        <p class="text-gray-400 text-sm">Total Tokens</p>
        <p class="text-2xl font-bold text-gray-100 mt-1">
          {totalTokens !== undefined ? formatTokenCount(totalTokens) : '--'}
        </p>
      </div>
    </div>
  );
}
