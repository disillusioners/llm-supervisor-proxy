interface ProxySettingsProps {
  upstreamUrl: string;
  upstreamToken: string;
  port: number;
  idleTimeout: string;
  maxUpstreamErrorRetries: number;
  maxIdleRetries: number;
  maxGenerationRetries: number;
  maxGenTime: string;
  originalPort: number;
  onUpstreamUrlChange: (value: string) => void;
  onUpstreamTokenChange: (value: string) => void;
  onPortChange: (value: number) => void;
  onIdleTimeoutChange: (value: string) => void;
  onMaxUpstreamErrorRetriesChange: (value: number) => void;
  onMaxIdleRetriesChange: (value: number) => void;
  onMaxGenerationRetriesChange: (value: number) => void;
  onMaxGenTimeChange: (value: string) => void;
  onApply: () => Promise<void>;
  status: { type: 'success' | 'error'; message: string; restartRequired?: boolean } | null;
  setStatus: (status: { type: 'success' | 'error'; message: string; restartRequired?: boolean } | null) => void;
}

export function ProxySettings({
  upstreamUrl,
  upstreamToken,
  port,
  idleTimeout,
  maxUpstreamErrorRetries,
  maxIdleRetries,
  maxGenerationRetries,
  maxGenTime,
  originalPort,
  onUpstreamUrlChange,
  onUpstreamTokenChange,
  onPortChange,
  onIdleTimeoutChange,
  onMaxUpstreamErrorRetriesChange,
  onMaxIdleRetriesChange,
  onMaxGenerationRetriesChange,
  onMaxGenTimeChange,
  onApply,
  status,
  setStatus,
}: ProxySettingsProps) {
  const handleApply = async () => {
    try {
      setStatus(null);
      await onApply();
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to update config' });
    }
  };

  return (
    <div class="space-y-4">
      {/* Upstream URL */}
      <div>
        <label class="block text-sm font-medium text-gray-300 mb-1">Upstream URL</label>
        <div class="relative">
          <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" />
            </svg>
          </span>
          <input
            type="text"
            value={upstreamUrl}
            onInput={(e) => onUpstreamUrlChange((e.target as HTMLInputElement).value)}
            class="w-full pl-10 pr-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="https://api.openai.com/v1"
          />
        </div>
      </div>

      {/* Upstream Token */}
      <div>
        <label class="block text-sm font-medium text-gray-300 mb-1">Upstream Token</label>
        <div class="relative">
          <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z" />
            </svg>
          </span>
          <input
            type="password"
            value={upstreamToken}
            onInput={(e) => onUpstreamTokenChange((e.target as HTMLInputElement).value)}
            class="w-full pl-10 pr-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="sk-..."
          />
        </div>
        <p class="text-xs text-gray-500 mt-1">API token for the external upstream (LiteLLM)</p>
      </div>

      {/* Port */}
      <div>
        <label class="block text-sm font-medium text-gray-300 mb-1 flex items-center justify-between">
          <span>Port</span>
          {port !== originalPort && (
            <span class="text-yellow-500 text-xs bg-yellow-500/10 px-2 py-0.5 rounded">⚠️ Requires restart</span>
          )}
        </label>
        <div class="relative">
          <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M12 5v14" />
            </svg>
          </span>
          <input
            type="number"
            value={port}
            onInput={(e) => onPortChange(parseInt((e.target as HTMLInputElement).value) || 8089)}
            class="w-full pl-10 pr-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="8089"
          />
        </div>
      </div>

      {/* Idle Timeout */}
      <div>
        <label class="block text-sm font-medium text-gray-300 mb-1">Idle Timeout</label>
        <div class="relative">
          <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
          </span>
          <input
            type="text"
            value={idleTimeout}
            onInput={(e) => onIdleTimeoutChange((e.target as HTMLInputElement).value)}
            class="w-full pl-10 pr-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="30s"
          />
        </div>
      </div>

      {/* Max Error Retries */}
      <div>
        <label class="block text-sm font-medium text-gray-300 mb-1">Max Error Retries</label>
        <div class="relative">
          <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
            </svg>
          </span>
          <input
            type="number"
            value={maxUpstreamErrorRetries}
            onInput={(e) => onMaxUpstreamErrorRetriesChange(parseInt((e.target as HTMLInputElement).value) || 0)}
            class="w-full pl-10 pr-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="3"
          />
        </div>
      </div>

      {/* Max Idle Retries */}
      <div>
        <label class="block text-sm font-medium text-gray-300 mb-1">Max Idle Retries</label>
        <div class="relative">
          <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
          </span>
          <input
            type="number"
            value={maxIdleRetries}
            onInput={(e) => onMaxIdleRetriesChange(parseInt((e.target as HTMLInputElement).value) || 0)}
            class="w-full pl-10 pr-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="2"
          />
        </div>
      </div>

      {/* Max Generation Retries */}
      <div>
        <label class="block text-sm font-medium text-gray-300 mb-1">Max Generation Retries</label>
        <div class="relative">
          <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
          </span>
          <input
            type="number"
            value={maxGenerationRetries}
            onInput={(e) => onMaxGenerationRetriesChange(parseInt((e.target as HTMLInputElement).value) || 0)}
            class="w-full pl-10 pr-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="2"
          />
        </div>
      </div>

      {/* Max Generation Time */}
      <div>
        <label class="block text-sm font-medium text-gray-300 mb-1">Max Generation Time</label>
        <div class="relative">
          <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
          </span>
          <input
            type="text"
            value={maxGenTime}
            onInput={(e) => onMaxGenTimeChange((e.target as HTMLInputElement).value)}
            class="w-full pl-10 pr-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="120s"
          />
        </div>
      </div>

      {/* Apply Button */}
      <div class="pt-2">
        <button
          onClick={handleApply}
          class="w-full bg-blue-600 hover:bg-blue-500 text-white font-medium py-2 px-4 rounded-md transition-colors shadow shadow-blue-900/20"
        >
          Apply Changes
        </button>
      </div>
    </div>
  );
}
