import { useState, useEffect, useCallback, useRef } from 'preact/hooks';
import type { Event } from '../types';

// Shared EventSource management to avoid redundant connections
let sharedEventSource: EventSource | null = null;
const subscribers = new Set<(data: Event) => void>();

function getSharedEventSource() {
  if (!sharedEventSource) {
    sharedEventSource = new EventSource('/fe/api/events');
    
    sharedEventSource.onmessage = (event) => {
      try {
        const data: Event = JSON.parse(event.data);
        subscribers.forEach((callback) => callback(data));
      } catch (e) {
        console.error('Failed to parse SSE event:', e);
      }
    };

    sharedEventSource.onerror = (err) => {
      // EventSource automatically reconnects. 
      // Only log if it's in a closed state to avoid noise during normal reconnection.
      if (sharedEventSource?.readyState === EventSource.CLOSED) {
        console.error('EventSource connection closed, attempting to reconnect...', err);
      }
    };
  }
  return sharedEventSource;
}

function subscribe(callback: (data: Event) => void) {
  subscribers.add(callback);
  getSharedEventSource();
  
  return () => {
    subscribers.delete(callback);
    if (subscribers.size === 0 && sharedEventSource) {
      sharedEventSource.close();
      sharedEventSource = null;
    }
  };
}

export function useEvents(selectedRequestId: string | null, autoScroll: boolean) {
  const [eventsMap, setEventsMap] = useState<Record<string, Event[]>>({});
  const [displayedEvents, setDisplayedEvents] = useState<Event[]>([]);
  const containerRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const handleEvent = (data: Event) => {
      // Most events use 'id', but loop detection events use 'request_id'
      const reqId = data.data?.id || data.data?.request_id;
      if (reqId) {
        setEventsMap((prev) => ({
          ...prev,
          [reqId]: [...(prev[reqId] || []), data],
        }));
      }
    };

    return subscribe(handleEvent);
  }, []);

  // Update displayed events when selection changes
  useEffect(() => {
    if (selectedRequestId && eventsMap[selectedRequestId]) {
      setDisplayedEvents(eventsMap[selectedRequestId]);
    } else {
      setDisplayedEvents([]);
    }
  }, [selectedRequestId, eventsMap]);

  // Auto-scroll when new events arrive
  useEffect(() => {
    if (autoScroll && containerRef.current) {
      containerRef.current.scrollTop = containerRef.current.scrollHeight;
    }
  }, [displayedEvents, autoScroll]);

  const clearEvents = useCallback(() => {
    if (selectedRequestId) {
      setEventsMap((prev) => ({
        ...prev,
        [selectedRequestId]: [],
      }));
      setDisplayedEvents([]);
    }
  }, [selectedRequestId]);

  return {
    eventsMap,
    displayedEvents,
    containerRef,
    clearEvents,
  };
}

// Hook to detect events that should trigger request list refresh
export function useEventRefresh(onRefresh: () => void) {
  useEffect(() => {
    const handleEvent = (data: Event) => {
      const refreshTypes = [
        'request_started',
        'request_completed',
        'retry_attempt',
        'error_max_upstream_error_retries',
        'timeout_idle',
        'loop_interrupted',
        'fallback_triggered',
        'all_models_failed',
      ];
      if (refreshTypes.includes(data.type)) {
        onRefresh();
      }
    };

    return subscribe(handleEvent);
  }, [onRefresh]);
}
