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
      return `Error: Max retries exceeded${event.data?.error ? ` - ${event.data.error}` : ''}`;
    case 'error':
      return `Error: ${event.data?.error || 'Unknown error'}`;
    case 'request_completed':
      return 'Request completed successfully.';
    default:
      return '';
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
    case 'error_max_upstream_error_retries':
    case 'error':
    case 'timeout_idle':
      return 'text-red-400';
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
      return 'ERROR_MAX_RETRIES';
    case 'timeout_idle':
      return 'TIMEOUT_IDLE';
    case 'error':
      return 'ERROR';
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
                <span class="text-gray-300">{getEventMessage(event)}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
};
