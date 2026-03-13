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

const getEventMessage = (event: Event): string => {
  switch (event.type) {
    case 'request_started':
      return 'Processing new request...';
    case 'timeout_idle':
      return `Idle timeout detected (${event.data?.timeout || 'unknown'})`;
    case 'retry_attempt':
      return `Retrying request (Attempt ${event.data?.attempt || '?'})`;
    case 'error_max_upstream_error_retries':
      return `Max retries exceeded${event.data?.error ? ` - ${event.data.error}` : ''}`;
    case 'upstream_error':
      return `Upstream request failed: ${event.data?.error || 'Unknown error'}`;
    case 'upstream_error_status':
      return `Upstream returned HTTP ${event.data?.status || '?'}`;
    case 'upstream_error_status_retry':
      return `Retry failed with HTTP ${event.data?.status || '?'} (headers already sent)`;
    case 'stream_error':
      return `Stream error: ${event.data?.error || 'Unknown error'}`;
    case 'stream_error_chunk': {
      const rawPreview = event.data?.raw_data ? ` | Raw: ${event.data.raw_data}` : '';
      return `Stream error chunk detected: ${event.data?.error || 'Unknown error'}${rawPreview}`;
    }
    case 'stream_error_after_headers': {
      const bufInfo = event.data?.buffer_size ? ` (buffer: ${event.data.buffer_size} bytes)` : '';
      return `Stream error after headers: ${event.data?.error || 'Unknown error'}${bufInfo}`;
    }
    case 'error_deadline_exceeded':
      return 'Generation deadline exceeded';
    case 'stream_ended_unexpectedly':
      return 'Stream ended unexpectedly without [DONE]';
    case 'fallback_triggered':
      return `Fallback: ${event.data?.from_model || '?'} -> ${event.data?.to_model || '?'}`;
    case 'all_models_failed':
      return 'All models failed after retries';
    case 'error':
      return `Error: ${event.data?.error || 'Unknown error'}`;
    case 'request_completed':
      return 'Request completed successfully.';
    case 'loop_detected': {
      const d = event.data;
      const mode = d?.shadow_mode ? ' [shadow]' : '';
      return `Loop detected${mode}: ${d?.strategy || '?'} (${d?.severity || '?'}) - ${d?.evidence || 'No details'}`;
    }
    case 'loop_interrupted': {
      const d = event.data;
      return `Loop interrupted: ${d?.strategy || '?'} - ${d?.evidence || 'Stream stopped, retrying with sanitized context'}`;
    }
    case 'client_disconnected_during_retry':
      return `Client disconnected during retry (Attempt ${event.data?.attempt || '?'})`;
    case 'client_disconnected_during_scan':
      return `Client disconnected during stream scan (buffer: ${event.data?.buffer_size || 0} bytes)`;
    case 'client_disconnected_during_buffering':
      return `Client disconnected during buffering (buffer: ${event.data?.buffer_size || 0} bytes)`;
    case 'stream_chunk_deadline':
      return `Stream chunk deadline reached - flushing buffer (${event.data?.buffer_size || 0} bytes, deadline: ${event.data?.deadline || '?'}, elapsed: ${event.data?.elapsed || '?'})`;
    default:
      return `Event: ${event.type}`;
  }
};

const getEventColor = (type: EventType): string => {
  switch (type) {
    case 'request_started':
      return 'text-blue-400';
    case 'request_completed':
      return 'text-green-400';
    case 'retry_attempt':
      return 'text-purple-400';
    case 'fallback_triggered':
      return 'text-orange-400';
    case 'error_max_upstream_error_retries':
    case 'all_models_failed':
    case 'error':
      return 'text-red-400';
    case 'timeout_idle':
    case 'error_deadline_exceeded':
    case 'upstream_error':
    case 'upstream_error_status':
    case 'upstream_error_status_retry':
    case 'stream_error':
    case 'stream_error_chunk':
    case 'stream_error_after_headers':
    case 'stream_ended_unexpectedly':
    case 'client_disconnected_during_retry':
    case 'client_disconnected_during_scan':
    case 'client_disconnected_during_buffering':
    case 'stream_chunk_deadline':
      return 'text-yellow-400';
    case 'loop_detected':
      return 'text-amber-400';
    case 'loop_interrupted':
      return 'text-red-300';
    default:
      return 'text-gray-400';
  }
};

const getEventTypeLabel = (type: EventType): string => {
  switch (type) {
    case 'request_started':
      return 'REQUEST_STARTED';
    case 'request_completed':
      return 'REQUEST_COMPLETED';
    case 'retry_attempt':
      return 'RETRY_ATTEMPT';
    case 'error_max_upstream_error_retries':
      return 'MAX_RETRIES_EXCEEDED';
    case 'upstream_error':
      return 'UPSTREAM_ERROR';
    case 'upstream_error_status':
      return 'UPSTREAM_STATUS';
    case 'upstream_error_status_retry':
      return 'RETRY_STATUS_ERROR';
    case 'stream_error':
      return 'STREAM_ERROR';
    case 'stream_error_chunk':
      return 'STREAM_ERROR_CHUNK';
    case 'stream_error_after_headers':
      return 'STREAM_ERROR_AFTER_HEADERS';
    case 'error_deadline_exceeded':
      return 'DEADLINE_EXCEEDED';
    case 'stream_ended_unexpectedly':
      return 'UNEXPECTED_EOF';
    case 'fallback_triggered':
      return 'FALLBACK';
    case 'all_models_failed':
      return 'ALL_MODELS_FAILED';
    case 'timeout_idle':
      return 'TIMEOUT_IDLE';
    case 'error':
      return 'ERROR';
    case 'loop_detected':
      return 'LOOP_DETECTED';
    case 'loop_interrupted':
      return 'LOOP_INTERRUPTED';
    case 'client_disconnected_during_retry':
      return 'CLIENT_DISCONNECTED';
    case 'client_disconnected_during_scan':
      return 'CLIENT_DISCONNECTED_SCAN';
    case 'client_disconnected_during_buffering':
      return 'CLIENT_DISCONNECTED_BUFFERING';
    case 'stream_chunk_deadline':
      return 'STREAM_CHUNK_DEADLINE';
    default:
      return String(type).toUpperCase();
  }
};

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
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
};
