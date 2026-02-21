import { useState, useEffect, useCallback, useRef } from 'preact/hooks';
import type { Event } from '../types';

export function useEvents(selectedRequestId: string | null, autoScroll: boolean) {
  const [eventsMap, setEventsMap] = useState<Record<string, Event[]>>({});
  const [displayedEvents, setDisplayedEvents] = useState<Event[]>([]);
  const eventSourceRef = useRef<EventSource | null>(null);
  const containerRef = useRef<HTMLDivElement | null>(null);

  // Initialize EventSource connection
  useEffect(() => {
    eventSourceRef.current = new EventSource('/api/events');

    eventSourceRef.current.onmessage = (event) => {
      const data: Event = JSON.parse(event.data);
      const reqId = data.data?.id;

      if (reqId) {
        setEventsMap((prev) => ({
          ...prev,
          [reqId]: [...(prev[reqId] || []), data],
        }));
      }
    };

    eventSourceRef.current.onerror = (err) => {
      console.error('EventSource error:', err);
    };

    return () => {
      eventSourceRef.current?.close();
    };
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
    const es = new EventSource('/api/events');

    es.onmessage = (event) => {
      const data: Event = JSON.parse(event.data);
      const refreshTypes = [
        'request_started',
        'request_completed',
        'retry_attempt',
        'error_max_retries',
        'timeout_idle',
      ];
      if (refreshTypes.includes(data.type)) {
        onRefresh();
      }
    };

    return () => es.close();
  }, [onRefresh]);
}
