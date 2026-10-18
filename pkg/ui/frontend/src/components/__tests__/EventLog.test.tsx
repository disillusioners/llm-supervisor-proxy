import { describe, it, expect, afterEach } from 'vitest';
import { render, cleanup } from '@testing-library/preact';
import { EventLog } from '../EventLog';
import type { Event, EventData } from '../../types';

afterEach(() => cleanup());

// EventLog's container ref is only used for scroll anchoring; happy-dom
// provides no layout engine, so a plain object ref suffices here.
function renderEvents(events: Event[]): Element {
  const { container } = render(
    <EventLog
      events={events}
      autoScroll={true}
      onToggleAutoScroll={() => {}}
      onClear={() => {}}
      containerRef={{ current: null }}
    />,
  );
  return container;
}

// All EventData fields are optional, so a Partial<EventData> payload
// assigns to Event.data directly — no double-cast needed, and the
// fixtures are type-checked against the real shape (incl. the additive
// race-reason keys reason/reason_class/reason_status).
function evt(type: Event['type'], data: Partial<EventData>): Event {
  return { type, timestamp: 0, data };
}

describe('EventLog race reason classification (additive renderers)', () => {
  it('race_spawn keeps the legacy line when no classified reason is present', () => {
    const root = renderEvents([
      evt('race_spawn', { type: 'fallback', request_index: 1, model: 'coding2', trigger: 'main_error' }),
    ]);
    expect(root.textContent).toContain('Spawned fallback request #1: coding2 (main_error)');
    expect(root.textContent).not.toContain('·');
  });

  it('race_spawn appends the classified reason additively when present', () => {
    const root = renderEvents([
      evt('race_spawn', {
        type: 'fallback',
        request_index: 1,
        model: 'coding2',
        trigger: 'main_error',
        reason: 'reason=upstream_5xx status=502 detail="…"',
        reason_class: 'upstream_5xx',
        reason_status: 502,
      }),
    ]);
    expect(root.textContent).toContain(
      'Spawned fallback request #1: coding2 (main_error · upstream_5xx status=502)',
    );
  });

  it('race_request_failed renders model, class, status, and bounded detail', () => {
    const root = renderEvents([
      evt('race_request_failed', {
        request_index: 0,
        model: 'm1',
        type: 'main',
        error: 'upstream returned error: 429 Too Many Requests',
        reason: 'reason=rate_limit status=429 detail="…"',
        reason_class: 'rate_limit',
        reason_status: 429,
      }),
    ]);
    expect(root.textContent).toContain(
      'Race request failed: m1 #0 (rate_limit status=429) - upstream returned error: 429 Too Many Requests',
    );
  });
});
