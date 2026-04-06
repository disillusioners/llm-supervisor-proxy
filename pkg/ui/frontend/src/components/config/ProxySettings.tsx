import type { Credential, Model, AppConfig } from '../../types';

interface ProxySettingsProps {
  upstreamUrl: string;
  upstreamCredentialId: string;
  credentials: Credential[];
  models: Model[];
  port: number;
  idleTimeout: string;
  streamDeadline: string;
  maxGenTime: string;
  originalPort: number;
  // Idle termination fields
  idleTerminationEnabled: boolean;
  idleTerminationTimeout: string;
  // Race retry fields
  raceRetryEnabled: boolean;
  raceParallelOnIdle: boolean;
  raceMaxParallel: number;
  raceMaxBufferBytes: number;
  // Ultimate model fields
  ultimateModelId: string;
  ultimateModelMaxHash: number;
  ultimateModelMaxRetries: number;
  // Raw response logging fields
  logRawUpstreamResponse: boolean;
  logRawUpstreamOnError: boolean;
  logRawUpstreamMaxKB: number;
  // Handlers
  onUpstreamUrlChange: (value: string) => void;
  onUpstreamCredentialIdChange: (value: string) => void;
  onPortChange: (value: number) => void;
  onIdleTimeoutChange: (value: string) => void;
  onStreamDeadlineChange: (value: string) => void;
  onMaxGenTimeChange: (value: string) => void;
  onRaceRetryEnabledChange: (value: boolean) => void;
  onRaceParallelOnIdleChange: (value: boolean) => void;
  onRaceMaxParallelChange: (value: number) => void;
  onRaceMaxBufferBytesChange: (value: number) => void;
  onUltimateModelIdChange: (value: string) => void;
  onUltimateModelMaxHashChange: (value: number) => void;
  onUltimateModelMaxRetriesChange: (value: number) => void;
  onLogRawUpstreamResponseChange: (value: boolean) => void;
  onLogRawUpstreamOnErrorChange: (value: boolean) => void;
  onLogRawUpstreamMaxKBChange: (value: number) => void;
  onIdleTerminationEnabledChange: (value: boolean) => void;
  onIdleTerminationTimeoutChange: (value: string) => void;
  onApply: () => Promise<void>;
  setStatus: (status: { type: 'success' | 'error'; message: string; restartRequired?: boolean } | null) => void;
}

export function ProxySettings({
  upstreamUrl,
  upstreamCredentialId,
  credentials,
  models,
  port,
  idleTimeout,
  streamDeadline,
  maxGenTime,
  originalPort,
  idleTerminationEnabled,
  idleTerminationTimeout,
  raceRetryEnabled,
  raceParallelOnIdle,
  raceMaxParallel,
  raceMaxBufferBytes,
  ultimateModelId,
  ultimateModelMaxHash,
  ultimateModelMaxRetries,
  logRawUpstreamResponse,
  logRawUpstreamOnError,
  logRawUpstreamMaxKB,
  onUpstreamUrlChange,
  onUpstreamCredentialIdChange,
  onPortChange,
  onIdleTimeoutChange,
  onStreamDeadlineChange,
  onMaxGenTimeChange,
  onRaceRetryEnabledChange,
  onRaceParallelOnIdleChange,
  onRaceMaxParallelChange,
  onRaceMaxBufferBytesChange,
  onUltimateModelIdChange,
  onUltimateModelMaxHashChange,
  onUltimateModelMaxRetriesChange,
  onLogRawUpstreamResponseChange,
  onLogRawUpstreamOnErrorChange,
  onLogRawUpstreamMaxKBChange,
  onIdleTerminationEnabledChange,
  onIdleTerminationTimeoutChange,
  onApply,
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

      {/* Upstream Credential */}
      <div>
        <label class="block text-sm font-medium text-gray-300 mb-1">
          Upstream Credential
        </label>
        <select
          value={upstreamCredentialId}
          onChange={(e) => onUpstreamCredentialIdChange((e.target as HTMLSelectElement).value)}
          class="w-full bg-gray-700 border border-gray-600 rounded px-3 py-2 text-white focus:outline-none focus:border-blue-500"
        >
          <option value="">None (use client's token)</option>
          {credentials.map((cred) => (
            <option key={cred.id} value={cred.id}>
              {cred.id} ({cred.provider})
            </option>
          ))}
        </select>
        <p class="text-xs text-gray-400 mt-1">
          Select a credential to use when forwarding requests to external upstream.
        </p>
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

      {/* Stream Deadline */}
      <div>
        <label class="block text-sm font-medium text-gray-300 mb-1">Stream Deadline</label>
        <div class="relative">
          <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
          </span>
          <input
            type="text"
            value={streamDeadline}
            onInput={(e) => onStreamDeadlineChange((e.target as HTMLInputElement).value)}
            class="w-full pl-10 pr-4 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="1m50s"
          />
        </div>
        <p class="text-xs text-gray-500 mt-1">
          Max buffer caching time for race retry. After this deadline, the request with most content wins and others are cancelled.
        </p>
      </div>

      {/* Idle Termination */}
      <div>
        <label class="block text-sm font-medium text-gray-300 mb-1">Enable Idle Stream Termination</label>
        <div class="flex items-center gap-3">
          <button
            type="button"
            onClick={() => onIdleTerminationEnabledChange(!idleTerminationEnabled)}
            class={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
              idleTerminationEnabled ? 'bg-blue-600' : 'bg-gray-600'
            }`}
          >
            <span class={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
              idleTerminationEnabled ? 'translate-x-6' : 'translate-x-1'
            }`} />
          </button>
          <span class="text-sm text-gray-400">
            {idleTerminationEnabled ? 'Enabled' : 'Disabled'}
          </span>
        </div>
      </div>

      {/* Idle Termination Timeout */}
      <div class={`${!idleTerminationEnabled ? 'opacity-50' : ''}`}>
        <label class="block text-sm font-medium text-gray-300 mb-1">Idle Termination Timeout</label>
        <div class="relative">
          <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
          </span>
          <input
            type="text"
            value={idleTerminationTimeout}
            onInput={(e) => onIdleTerminationTimeoutChange((e.target as HTMLInputElement).value)}
            disabled={!idleTerminationEnabled}
            class={`w-full pl-10 pr-4 py-2 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow ${
              idleTerminationEnabled ? 'bg-gray-700' : 'bg-gray-800'
            }`}
            placeholder="2m"
          />
        </div>
        <p class="text-xs text-gray-500 mt-1">
          Max idle time after stream winner is selected. If upstream sends no data for this duration, the stream is terminated.
        </p>
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

      {/* Race Retry Section */}
      <div class="border-t border-gray-700 pt-4 mt-4">
        <h3 class="text-sm font-medium text-gray-200 mb-3">Race Retry (Parallel Requests)</h3>
        <p class="text-xs text-gray-400 mb-3">
          When enabled, multiple upstream requests race in parallel. The first to complete wins.
        </p>

        {/* Enable Race Retry */}
        <div class="mb-3">
          <label class="block text-sm font-medium text-gray-300 mb-1">Enable Race Retry</label>
          <div class="flex items-center gap-3">
            <button
              type="button"
              onClick={() => onRaceRetryEnabledChange(!raceRetryEnabled)}
              class={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
                raceRetryEnabled ? 'bg-blue-600' : 'bg-gray-600'
              }`}
            >
              <span class={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                raceRetryEnabled ? 'translate-x-6' : 'translate-x-1'
              }`} />
            </button>
            <span class="text-sm text-gray-400">
              {raceRetryEnabled ? 'Enabled' : 'Disabled'}
            </span>
          </div>
        </div>

        {/* Parallel on Idle */}
        <div class="mb-3">
          <label class="block text-sm font-medium text-gray-300 mb-1">Spawn Parallel on Idle</label>
          <div class="flex items-center gap-3">
            <button
              type="button"
              onClick={() => onRaceParallelOnIdleChange(!raceParallelOnIdle)}
              class={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
                raceParallelOnIdle ? 'bg-blue-600' : 'bg-gray-600'
              }`}
            >
              <span class={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                raceParallelOnIdle ? 'translate-x-6' : 'translate-x-1'
              }`} />
            </button>
            <span class="text-sm text-gray-400">
              {raceParallelOnIdle ? 'Enabled' : 'Disabled'}
            </span>
          </div>
          <p class="text-xs text-gray-500 mt-1">
            When main request hits idle timeout, spawn parallel requests instead of cancelling.
          </p>
        </div>

        {/* Max Parallel Requests */}
        <div class="mb-3">
          <label class="block text-sm font-medium text-gray-300 mb-1">Max Parallel Requests</label>
          <input
            type="number"
            value={raceMaxParallel}
            onInput={(e) => onRaceMaxParallelChange(parseInt((e.target as HTMLInputElement).value) || 3)}
            class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="3"
            min="1"
            max="5"
          />
          <p class="text-xs text-gray-500 mt-1">
            Maximum concurrent requests (main + second + fallback). Default: 3
          </p>
        </div>

        {/* Max Buffer Bytes */}
        <div class="mb-3">
          <label class="block text-sm font-medium text-gray-300 mb-1">Max Buffer Per Request (MB)</label>
          <input
            type="number"
            value={Math.round(raceMaxBufferBytes / (1024 * 1024))}
            onInput={(e) => onRaceMaxBufferBytesChange(parseInt((e.target as HTMLInputElement).value) * 1024 * 1024 || 5242880)}
            class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="5"
            min="1"
            max="50"
          />
          <p class="text-xs text-gray-500 mt-1">
            Maximum bytes to buffer per request. Default: 5MB
          </p>
        </div>
      </div>

      {/* Ultimate Model Section */}
      <div class="border-t border-gray-700 pt-4 mt-4">
        <h3 class="text-sm font-medium text-gray-200 mb-3">Ultimate Model</h3>
        <p class="text-xs text-gray-400 mb-3">
          When a duplicate request is detected, the proxy will bypass all normal logic
          (fallback, retry, buffering) and use this model as a raw proxy.
        </p>

        {/* Ultimate Model ID */}
        <div class="mb-3">
          <label class="block text-sm font-medium text-gray-300 mb-1">Ultimate Model ID</label>
          <select
            value={ultimateModelId}
            onChange={(e) => onUltimateModelIdChange((e.target as HTMLSelectElement).value)}
            class="w-full bg-gray-700 border border-gray-600 rounded px-3 py-2 text-white focus:outline-none focus:border-blue-500"
          >
            <option value="">None (disabled)</option>
            {models.filter(m => m.enabled).map((model) => (
              <option key={model.id} value={model.id}>
                {model.name || model.id}
              </option>
            ))}
          </select>
          <p class="text-xs text-gray-500 mt-1">
            Select a model to use for duplicate request handling. Leave empty to disable.
          </p>
        </div>

        {/* Max Hash Cache Size */}
        <div class="mb-3">
          <label class="block text-sm font-medium text-gray-300 mb-1">Max Hash Cache Size</label>
          <input
            type="number"
            value={ultimateModelMaxHash}
            onInput={(e) => onUltimateModelMaxHashChange(parseInt((e.target as HTMLInputElement).value) || 100)}
            class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="100"
            min="1"
            max="10000"
          />
          <p class="text-xs text-gray-500 mt-1">
            Maximum number of request hashes to remember for duplicate detection.
            Uses circular buffer (oldest removed when full).
          </p>
        </div>

        {/* Max Retries */}
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Ultimate Model Max Retries</label>
          <input
            type="number"
            value={ultimateModelMaxRetries}
            onInput={(e) => {
              const val = parseInt((e.target as HTMLInputElement).value) || 0;
              // Allow 0-100 to match backend validation
              if (val >= 0 && val <= 100) {
                onUltimateModelMaxRetriesChange(val);
              }
            }}
            class={`w-full px-3 py-2 bg-gray-800 border rounded text-white ${
              ultimateModelMaxRetries < 0 || ultimateModelMaxRetries > 100 ? 'border-red-500' :
              ultimateModelMaxRetries > 10 ? 'border-yellow-500' : 'border-gray-700'
            }`}
            min="0"
            max="100"
          />
          {ultimateModelMaxRetries < 0 && (
            <p class="text-red-500 text-xs mt-1">Value cannot be negative</p>
          )}
          {ultimateModelMaxRetries > 100 && (
            <p class="text-red-500 text-xs mt-1">Value cannot exceed 100</p>
          )}
          {ultimateModelMaxRetries > 10 && ultimateModelMaxRetries <= 100 && (
            <p class="text-yellow-500 text-xs mt-1">⚠️ High values may cause long retry loops</p>
          )}
          <p class="text-xs text-gray-500 mt-1">
            Maximum number of times the ultimate model can be retried for the same request hash.
            Set to 0 to disable retry limit (not recommended).
          </p>
        </div>
      </div>

      {/* Debug & Logging Section */}
      <div class="border-t border-gray-700 pt-4 mt-4">
        <h3 class="text-sm font-medium text-gray-200 mb-3">Debug & Logging</h3>
        <p class="text-xs text-gray-400 mb-3">
          Save raw upstream responses to files for debugging. Requires buffer_storage_dir to be configured.
        </p>

        {/* Log Successful Responses */}
        <div class="mb-3">
          <label class="block text-sm font-medium text-gray-300 mb-1">Log Successful Responses</label>
          <div class="flex items-center gap-3">
            <button
              type="button"
              onClick={() => onLogRawUpstreamResponseChange(!logRawUpstreamResponse)}
              class={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
                logRawUpstreamResponse ? 'bg-blue-600' : 'bg-gray-600'
              }`}
            >
              <span class={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                logRawUpstreamResponse ? 'translate-x-6' : 'translate-x-1'
              }`} />
            </button>
            <span class="text-sm text-gray-400">
              {logRawUpstreamResponse ? 'Enabled' : 'Disabled'}
            </span>
          </div>
          <p class="text-xs text-gray-500 mt-1">
            Save raw upstream responses for successful requests.
          </p>
        </div>

        {/* Log Failed Responses */}
        <div class="mb-3">
          <label class="block text-sm font-medium text-gray-300 mb-1">Log Failed Responses</label>
          <div class="flex items-center gap-3">
            <button
              type="button"
              onClick={() => onLogRawUpstreamOnErrorChange(!logRawUpstreamOnError)}
              class={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
                logRawUpstreamOnError ? 'bg-blue-600' : 'bg-gray-600'
              }`}
            >
              <span class={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                logRawUpstreamOnError ? 'translate-x-6' : 'translate-x-1'
              }`} />
            </button>
            <span class="text-sm text-gray-400">
              {logRawUpstreamOnError ? 'Enabled' : 'Disabled'}
            </span>
          </div>
          <p class="text-xs text-gray-500 mt-1">
            Save raw upstream responses for failed/error requests.
          </p>
        </div>

        {/* Max Response Size */}
        <div class="mb-3">
          <label class="block text-sm font-medium text-gray-300 mb-1">Max Response Size (KB)</label>
          <input
            type="number"
            value={logRawUpstreamMaxKB}
            onInput={(e) => onLogRawUpstreamMaxKBChange(parseInt((e.target as HTMLInputElement).value) || 1024)}
            class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="1024"
            min="1"
            max="102400"
          />
          <p class="text-xs text-gray-500 mt-1">
            Maximum response size to log. Larger responses are skipped. Default: 1024 KB (1 MB)
          </p>
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
