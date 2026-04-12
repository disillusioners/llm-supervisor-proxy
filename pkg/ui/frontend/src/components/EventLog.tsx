import { FunctionComponent, Ref } from 'preact';
import { Event, EventType } from '../types';
import { formatTime } from '../utils/helpers';

interface EventLogProps {
  events: Event[];
  autoScroll: boolean;
  onToggleAutoScroll: () => void;
  onClear: () => void;
  containerRef: Ref<HTMLDivElement>;
}

// Pre-computed maps for O(1) lookups instead of O(n) switch evaluation on every render
const EVENT_MESSAGES: Record<EventType, (event: Event) => string> = {
  request_started: () => 'Processing new request...',
  timeout_idle: (e) => `Idle timeout detected (${e.data?.timeout || 'unknown'})`,
  retry_attempt: (e) => `Retrying request (Attempt ${e.data?.attempt || '?'})`,
  error_max_upstream_error_retries: (e) => `Max retries exceeded${e.data?.error ? ` - ${e.data.error}` : ''}`,
  upstream_error: (e) => `Upstream request failed: ${e.data?.error || 'Unknown error'}`,
  upstream_error_status: (e) => `Upstream returned HTTP ${e.data?.status || '?'}`,
  upstream_error_status_retry: (e) => `Retry failed with HTTP ${e.data?.status || '?'} (headers already sent)`,
  stream_error: (e) => `Stream error: ${e.data?.error || 'Unknown error'}`,
  stream_error_chunk: (e) => {
    const rawPreview = e.data?.raw_data ? ` | Raw: ${e.data.raw_data}` : '';
    return `Stream error chunk detected: ${e.data?.error || 'Unknown error'}${rawPreview}`;
  },
  stream_error_after_headers: (e) => {
    const bufInfo = e.data?.buffer_size ? ` (buffer: ${e.data.buffer_size} bytes)` : '';
    return `Stream error after headers: ${e.data?.error || 'Unknown error'}${bufInfo}`;
  },
  error_deadline_exceeded: () => 'Generation deadline exceeded',
  stream_ended_unexpectedly: () => 'Stream ended unexpectedly without [DONE]',
  fallback_triggered: (e) => `Fallback: ${e.data?.from_model || '?'} -> ${e.data?.to_model || '?'}`,
  all_models_failed: () => 'All models failed after retries',
  error: (e) => `Error: ${e.data?.error || 'Unknown error'}`,
  request_completed: () => 'Request completed successfully.',
  response_logged: (e) => {
    const size = e.data?.size_bytes ? ` (${(e.data.size_bytes / 1024).toFixed(1)} KB)` : '';
    return `Raw response logged${size}`;
  },
  loop_detected: (e) => {
    const mode = e.data?.shadow_mode ? ' [shadow]' : '';
    return `Loop detected${mode}: ${e.data?.strategy || '?'} (${e.data?.severity || '?'}) - ${e.data?.evidence || 'No details'}`;
  },
  loop_interrupted: (e) => `Loop interrupted: ${e.data?.strategy || '?'} - ${e.data?.evidence || 'Stream stopped, retrying with sanitized context'}`,
  client_disconnected: () => 'Client disconnected',
  client_disconnected_during_retry: (e) => `Client disconnected during retry (Attempt ${e.data?.attempt || '?'})`,
  client_disconnected_during_scan: (e) => `Client disconnected during stream scan (buffer: ${e.data?.buffer_size || 0} bytes)`,
  client_disconnected_during_buffering: (e) => `Client disconnected during buffering (buffer: ${e.data?.buffer_size || 0} bytes)`,
  client_disconnected_during_stream: () => 'Client disconnected during stream read',
  client_disconnected_during_internal: () => 'Client disconnected during internal request',
  stream_chunk_deadline: (e) => `Stream chunk deadline reached - flushing buffer (${e.data?.buffer_size || 0} bytes, deadline: ${e.data?.deadline || '?'}, elapsed: ${e.data?.elapsed || '?'})`,
  stream_normalize: (e) => {
    const provider = e.data?.provider ? ` [${e.data.provider}]` : '';
    return `Stream normalized${provider}: ${e.data?.description || e.data?.normalizer || 'unknown fix'}`;
  },
  shadow_retry_started: (e) => {
    const trigger = e.data?.trigger ? ` (${e.data.trigger})` : '';
    return `Shadow retry started: ${e.data?.model || '?'} vs ${e.data?.main_model || 'main'}${trigger}`;
  },
  shadow_retry_won: (e) => {
    const dur = e.data?.duration ? ` in ${e.data.duration}` : '';
    return `Shadow retry won: ${e.data?.model || '?'} completed faster than ${e.data?.main_model || 'main'}${dur}`;
  },
  shadow_retry_failed: (e) => `Shadow retry failed: ${e.data?.model || '?'} - ${e.data?.error || 'Unknown error'}`,
  shadow_retry_lost: (e) => {
    const dur = e.data?.duration ? ` after ${e.data.duration}` : '';
    return `Shadow retry lost: ${e.data?.main_model || 'main'} completed before ${e.data?.model || '?'}${dur}`;
  },
  ultimate_model_triggered: (e) => {
    const hash = e.data?.hash ? ` (${e.data.hash.substring(0, 8)}...)` : '';
    return `Ultimate model triggered: ${e.data?.original_model || '?'} -> ${e.data?.ultimate_model || '?'}${hash}`;
  },
  ultimate_model_failed: (e) => `Ultimate model failed: ${e.data?.ultimate_model || '?'} - ${e.data?.error || 'Unknown error'}`,
  race_started: (e) => `Race started with models: ${e.data?.models?.join(', ') || '?'}`,
  race_spawn: (e) => {
    const trigger = e.data?.trigger ? ` (${e.data.trigger})` : '';
    return `Spawned ${e.data?.type || '?'} request #${e.data?.request_index ?? '?'}: ${e.data?.model || '?'}${trigger}`;
  },
  race_winner_selected: (e) => {
    const duration = e.data?.duration_ms ? ` in ${e.data.duration_ms}ms` : '';
    const bytes = e.data?.buffer_bytes ? ` (${(e.data.buffer_bytes / 1024).toFixed(1)}KB)` : '';
    return `Winner: ${e.data?.winner_type || '?'} request #${e.data?.winner_index ?? '?'} (${e.data?.winner_model || '?'})${duration}${bytes}`;
  },
  race_all_failed: (e) => `All ${e.data?.total_attempts || '?'} race requests failed after ${e.data?.duration_ms || '?'}ms`,
  race_secondary_model_used: (e) => `Switched to secondary upstream model: ${e.data?.secondary_model || '?'}`,
  tool_repair: (e) => {
    const d = e.data;
    const repaired = d?.repaired ?? 0;
    const failed = d?.failed ?? 0;
    const total = d?.total_tool_calls ?? 0;
    const strategies = d?.strategies_used?.join(', ') || 'none';
    return `Tool repair: ${repaired}/${total} repaired, ${failed} failed (strategies: ${strategies})`;
  },
  ultimate_model_retry_exhausted: (e) => {
    const d = e.data;
    return `Ultimate model retries exhausted: ${d?.current_retry || '?'}/${d?.max_retries || '?'}`;
  },
  internal_error: (e) => `Internal error: ${e.data?.error || 'Unknown error'}`,
};

const EVENT_COLORS: Record<EventType, string> = {
  request_started: 'text-blue-400',
  request_completed: 'text-green-400',
  response_logged: 'text-cyan-400',
  retry_attempt: 'text-purple-400',
  fallback_triggered: 'text-orange-400',
  error_max_upstream_error_retries: 'text-red-400',
  all_models_failed: 'text-red-400',
  error: 'text-red-400',
  timeout_idle: 'text-yellow-400',
  error_deadline_exceeded: 'text-yellow-400',
  upstream_error: 'text-yellow-400',
  upstream_error_status: 'text-yellow-400',
  upstream_error_status_retry: 'text-yellow-400',
  stream_error: 'text-yellow-400',
  stream_error_chunk: 'text-yellow-400',
  stream_error_after_headers: 'text-yellow-400',
  stream_ended_unexpectedly: 'text-yellow-400',
  client_disconnected: 'text-yellow-400',
  client_disconnected_during_retry: 'text-yellow-400',
  client_disconnected_during_scan: 'text-yellow-400',
  client_disconnected_during_buffering: 'text-yellow-400',
  client_disconnected_during_stream: 'text-yellow-400',
  client_disconnected_during_internal: 'text-yellow-400',
  stream_chunk_deadline: 'text-yellow-400',
  loop_detected: 'text-amber-400',
  loop_interrupted: 'text-red-300',
  shadow_retry_started: 'text-cyan-400',
  shadow_retry_won: 'text-green-400',
  shadow_retry_failed: 'text-red-400',
  shadow_retry_lost: 'text-gray-400',
  ultimate_model_triggered: 'text-pink-400',
  ultimate_model_failed: 'text-red-400',
  stream_normalize: 'text-amber-400',
  race_started: 'text-cyan-400',
  race_spawn: 'text-blue-400',
  race_winner_selected: 'text-green-400',
  race_all_failed: 'text-red-400',
  race_secondary_model_used: 'text-purple-400',
  tool_repair: 'text-amber-400',
  ultimate_model_retry_exhausted: 'text-red-400',
  internal_error: 'text-red-400',
};

const EVENT_TYPE_LABELS: Record<EventType, string> = {
  request_started: 'REQUEST_STARTED',
  request_completed: 'REQUEST_COMPLETED',
  response_logged: 'RESPONSE_LOGGED',
  retry_attempt: 'RETRY_ATTEMPT',
  error_max_upstream_error_retries: 'MAX_RETRIES_EXCEEDED',
  upstream_error: 'UPSTREAM_ERROR',
  upstream_error_status: 'UPSTREAM_STATUS',
  upstream_error_status_retry: 'RETRY_STATUS_ERROR',
  stream_error: 'STREAM_ERROR',
  stream_error_chunk: 'STREAM_ERROR_CHUNK',
  stream_error_after_headers: 'STREAM_ERROR_AFTER_HEADERS',
  error_deadline_exceeded: 'DEADLINE_EXCEEDED',
  stream_ended_unexpectedly: 'UNEXPECTED_EOF',
  fallback_triggered: 'FALLBACK',
  all_models_failed: 'ALL_MODELS_FAILED',
  timeout_idle: 'TIMEOUT_IDLE',
  error: 'ERROR',
  loop_detected: 'LOOP_DETECTED',
  loop_interrupted: 'LOOP_INTERRUPTED',
  client_disconnected: 'CLIENT_DISCONNECTED',
  client_disconnected_during_retry: 'CLIENT_DISCONNECTED',
  client_disconnected_during_scan: 'CLIENT_DISCONNECTED_SCAN',
  client_disconnected_during_buffering: 'CLIENT_DISCONNECTED_BUFFERING',
  client_disconnected_during_stream: 'CLIENT_DISCONNECTED_STREAM',
  client_disconnected_during_internal: 'CLIENT_DISCONNECTED_INTERNAL',
  stream_chunk_deadline: 'STREAM_CHUNK_DEADLINE',
  stream_normalize: 'STREAM_NORMALIZE',
  shadow_retry_started: 'SHADOW_RETRY_STARTED',
  shadow_retry_won: 'SHADOW_RETRY_WON',
  shadow_retry_failed: 'SHADOW_RETRY_FAILED',
  shadow_retry_lost: 'SHADOW_RETRY_LOST',
  ultimate_model_triggered: 'ULTIMATE_MODEL',
  ultimate_model_failed: 'ULTIMATE_MODEL_FAILED',
  race_started: 'RACE_STARTED',
  race_spawn: 'RACE_SPAWN',
  race_winner_selected: 'RACE_WINNER',
  race_all_failed: 'RACE_ALL_FAILED',
  race_secondary_model_used: 'SECONDARY_MODEL_USED',
  tool_repair: 'TOOL_REPAIR',
  ultimate_model_retry_exhausted: 'ULTIMATE_RETRY_EXHAUSTED',
  internal_error: 'INTERNAL_ERROR',
};

// Fast O(1) lookup helpers - no more switch statements
const getEventMessage = (event: Event): string => {
  const handler = EVENT_MESSAGES[event.type];
  return handler ? handler(event) : `Event: ${event.type}`;
};

const getEventColor = (type: EventType): string => EVENT_COLORS[type] || 'text-gray-400';

const getEventTypeLabel = (type: EventType): string => EVENT_TYPE_LABELS[type] || String(type).toUpperCase();

export const EventLog: FunctionComponent<EventLogProps> = ({
  events,
  autoScroll,
  onToggleAutoScroll,
  onClear,
  containerRef,
}) => {
  return (
    <div class="flex-1 bg-[#0d1117] flex flex-col min-h-0">
      {/* Header */}
      <div class="flex items-center justify-between px-3 py-2 border-b border-gray-700 shrink-0">
        <div class="flex items-center gap-2">
          <svg
            class="w-4 h-4 text-green-400"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M9 17v-2m3 2v-4m3 4v-6m2 10H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"
            />
          </svg>
          <span class="text-sm font-medium text-gray-200">Event Log</span>
        </div>
        <div class="flex items-center gap-1">
          {/* Auto-scroll toggle */}
          <button
            onClick={onToggleAutoScroll}
            class={`px-2 py-1 text-xs rounded transition-colors ${autoScroll
              ? 'bg-green-600 text-white'
              : 'bg-gray-600 text-gray-300 hover:bg-gray-500'
              }`}
            title={autoScroll ? 'Auto-scroll ON' : 'Auto-scroll OFF'}
          >
            Auto-scroll
          </button>
          {/* Clear button */}
          <button
            onClick={onClear}
            class="px-2 py-1 text-xs bg-gray-600 text-gray-300 rounded hover:bg-gray-500 transition-colors"
            title="Clear logs"
          >
            Clear
          </button>
        </div>
      </div>

      {/* Event list */}
      <div
        ref={containerRef}
        class="flex-1 overflow-y-auto min-h-0 p-2 monitor-font text-xs"
      >
        {events.length === 0 ? (
          <div class="text-gray-500 italic">Select a request to view logs...</div>
        ) : (
          <div class="space-y-1">
            {events.map((event, index) => (
              <div key={index} class="flex gap-2">
                <span class="text-gray-500 shrink-0">
                  [{formatTime(event.timestamp)}]
                </span>
                <span class={`shrink-0 font-semibold ${getEventColor(event.type)}`}>
                  [{getEventTypeLabel(event.type)}]
                </span>
                <span class="text-gray-300">
                  {getEventMessage(event)}
                  {(event.type === 'stream_error_after_headers' || event.type === 'upstream_error_status' || event.type === 'internal_error') && event.data?.buffer_id && (
                    <a
                      href={`/fe/api/buffers/${event.data.buffer_id}`}
                      target="_blank"
                      rel="noopener noreferrer"
                      class="text-blue-400 hover:underline ml-1"
                    >
                      [View Request]
                    </a>
                  )}
                  {event.type === 'response_logged' && event.data?.buffer_id && (
                    <a
                      href={`/fe/api/buffers/${event.data.buffer_id}`}
                      target="_blank"
                      rel="noopener noreferrer"
                      class="text-blue-400 hover:underline ml-1"
                    >
                      [View Response]
                    </a>
                  )}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
};
