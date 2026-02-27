import { useState, useEffect } from 'preact/hooks';
import type { LoopDetectionConfig } from '../../types';

interface LoopDetectionSettingsProps {
  config: LoopDetectionConfig | null;
  onApply: (config: LoopDetectionConfig) => Promise<void>;
  status: { type: 'success' | 'error'; message: string; restartRequired?: boolean } | null;
  setStatus: (status: { type: 'success' | 'error'; message: string; restartRequired?: boolean } | null) => void;
}

export function LoopDetectionSettings({
  config,
  onApply,
  setStatus,
}: LoopDetectionSettingsProps) {
  const [loopEnabled, setLoopEnabled] = useState(true);
  const [loopShadowMode, setLoopShadowMode] = useState(true);
  const [loopMessageWindow, setLoopMessageWindow] = useState(10);
  const [loopActionWindow, setLoopActionWindow] = useState(15);
  const [loopExactMatchCount, setLoopExactMatchCount] = useState(3);
  const [loopSimilarityThreshold, setLoopSimilarityThreshold] = useState(0.85);
  const [loopMinTokensSimhash, setLoopMinTokensSimhash] = useState(15);
  const [loopActionRepeatCount, setLoopActionRepeatCount] = useState(3);
  const [loopOscillationCount, setLoopOscillationCount] = useState(4);
  const [loopMinTokensAnalysis, setLoopMinTokensAnalysis] = useState(20);

  // Sync state when config changes
  useEffect(() => {
    if (config) {
      setLoopEnabled(config.enabled);
      setLoopShadowMode(config.shadow_mode);
      setLoopMessageWindow(config.message_window);
      setLoopActionWindow(config.action_window);
      setLoopExactMatchCount(config.exact_match_count);
      setLoopSimilarityThreshold(config.similarity_threshold);
      setLoopMinTokensSimhash(config.min_tokens_for_simhash);
      setLoopActionRepeatCount(config.action_repeat_count);
      setLoopOscillationCount(config.oscillation_count);
      setLoopMinTokensAnalysis(config.min_tokens_for_analysis);
    }
  }, [config]);

  const handleApply = async () => {
    try {
      setStatus(null);
      await onApply({
        enabled: loopEnabled,
        shadow_mode: loopShadowMode,
        message_window: loopMessageWindow,
        action_window: loopActionWindow,
        exact_match_count: loopExactMatchCount,
        similarity_threshold: loopSimilarityThreshold,
        min_tokens_for_simhash: loopMinTokensSimhash,
        action_repeat_count: loopActionRepeatCount,
        oscillation_count: loopOscillationCount,
        min_tokens_for_analysis: loopMinTokensAnalysis,
        // Phase 3 advanced detection (use current values from config)
        thinking_min_tokens: config?.thinking_min_tokens ?? 100,
        trigram_threshold: config?.trigram_threshold ?? 0.3,
        max_cycle_length: config?.max_cycle_length ?? 5,
        reasoning_model_patterns: config?.reasoning_model_patterns ?? ['o1', 'o3', 'deepseek-r1'],
        reasoning_trigram_threshold: config?.reasoning_trigram_threshold ?? 0.15,
      });
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to update loop detection config' });
    }
  };

  return (
    <div class="space-y-4">
      {/* Enable / Shadow Mode Toggles */}
      <div class="flex gap-6">
        <label class="flex items-center gap-2 cursor-pointer">
          <input
            type="checkbox"
            checked={loopEnabled}
            onInput={(e) => setLoopEnabled((e.target as HTMLInputElement).checked)}
            class="w-4 h-4 rounded border-gray-600 bg-gray-700 text-blue-500 focus:ring-blue-500 focus:ring-offset-gray-800"
          />
          <span class="text-gray-300">Enabled</span>
        </label>
        <label class="flex items-center gap-2 cursor-pointer">
          <input
            type="checkbox"
            checked={loopShadowMode}
            onInput={(e) => setLoopShadowMode((e.target as HTMLInputElement).checked)}
            class="w-4 h-4 rounded border-gray-600 bg-gray-700 text-blue-500 focus:ring-blue-500 focus:ring-offset-gray-800"
          />
          <span class="text-gray-300">Shadow Mode</span>
          <span class="text-xs text-gray-500">(log only, no interrupt)</span>
        </label>
      </div>

      {/* Info Box */}
      <div class="bg-blue-900/20 border border-blue-800/30 rounded-md p-3">
        <p class="text-sm text-blue-300">
          <strong>Loop Detection</strong> monitors LLM responses for repetitive patterns (identical messages, similar content, repeated tool calls).
          {loopShadowMode ? ' Currently in shadow mode - loops are logged but not interrupted.' : ' Will interrupt streams when loops are detected.'}
        </p>
      </div>

      {/* Detection Thresholds */}
      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Message Window</label>
          <input
            type="number"
            value={loopMessageWindow}
            onInput={(e) => setLoopMessageWindow(parseInt((e.target as HTMLInputElement).value) || 10)}
            class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
          <p class="text-xs text-gray-500 mt-1">Messages to keep in sliding window</p>
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Action Window</label>
          <input
            type="number"
            value={loopActionWindow}
            onInput={(e) => setLoopActionWindow(parseInt((e.target as HTMLInputElement).value) || 15)}
            class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
          <p class="text-xs text-gray-500 mt-1">Actions to keep in sliding window</p>
        </div>
      </div>

      {/* Exact Match Settings */}
      <div class="border-t border-gray-700 pt-4">
        <h4 class="text-sm font-medium text-gray-200 mb-3">Exact Match Detection</h4>
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Exact Match Count</label>
          <input
            type="number"
            value={loopExactMatchCount}
            onInput={(e) => setLoopExactMatchCount(parseInt((e.target as HTMLInputElement).value) || 2)}
            class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            min="2"
          />
          <p class="text-xs text-gray-500 mt-1">Identical messages to trigger detection</p>
        </div>
      </div>

      {/* Similarity Settings */}
      <div class="border-t border-gray-700 pt-4">
        <h4 class="text-sm font-medium text-gray-200 mb-3">Similarity Detection (SimHash)</h4>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="block text-sm font-medium text-gray-300 mb-1">Similarity Threshold</label>
            <input
              type="number"
              value={loopSimilarityThreshold}
              onInput={(e) => setLoopSimilarityThreshold(parseFloat((e.target as HTMLInputElement).value) || 0.85)}
              class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
              min="0"
              max="1"
              step="0.05"
            />
            <p class="text-xs text-gray-500 mt-1">0.85 = 85% similarity</p>
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-300 mb-1">Min Tokens for SimHash</label>
            <input
              type="number"
              value={loopMinTokensSimhash}
              onInput={(e) => setLoopMinTokensSimhash(parseInt((e.target as HTMLInputElement).value) || 15)}
              class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
              min="5"
            />
            <p class="text-xs text-gray-500 mt-1">Min tokens before SimHash applies</p>
          </div>
        </div>
      </div>

      {/* Action Pattern Settings */}
      <div class="border-t border-gray-700 pt-4">
        <h4 class="text-sm font-medium text-gray-200 mb-3">Action Pattern Detection</h4>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="block text-sm font-medium text-gray-300 mb-1">Action Repeat Count</label>
            <input
              type="number"
              value={loopActionRepeatCount}
              onInput={(e) => setLoopActionRepeatCount(parseInt((e.target as HTMLInputElement).value) || 3)}
              class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
              min="2"
            />
            <p class="text-xs text-gray-500 mt-1">Consecutive same actions to trigger</p>
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-300 mb-1">Oscillation Count</label>
            <input
              type="number"
              value={loopOscillationCount}
              onInput={(e) => setLoopOscillationCount(parseInt((e.target as HTMLInputElement).value) || 4)}
              class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
              min="2"
            />
            <p class="text-xs text-gray-500 mt-1">A↔B oscillations to trigger</p>
          </div>
        </div>
      </div>

      {/* Stream Processing */}
      <div class="border-t border-gray-700 pt-4">
        <h4 class="text-sm font-medium text-gray-200 mb-3">Stream Processing</h4>
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Min Tokens for Analysis</label>
          <input
            type="number"
            value={loopMinTokensAnalysis}
            onInput={(e) => setLoopMinTokensAnalysis(parseInt((e.target as HTMLInputElement).value) || 20)}
            class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            min="5"
          />
          <p class="text-xs text-gray-500 mt-1">Buffer tokens before running detection</p>
        </div>
      </div>

      {/* Apply Button */}
      <div class="pt-2">
        <button
          onClick={handleApply}
          class="w-full bg-blue-600 hover:bg-blue-500 text-white font-medium py-2 px-4 rounded-md transition-colors shadow shadow-blue-900/20"
        >
          Apply Loop Detection Settings
        </button>
      </div>
    </div>
  );
}
