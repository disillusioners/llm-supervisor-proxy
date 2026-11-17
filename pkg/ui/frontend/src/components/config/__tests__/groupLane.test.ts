import { describe, it, expect } from 'vitest';
import {
  normalizeKind,
  buildKindIndex,
  inferGroupLane,
  splitGroupsByLane,
} from '../groupLane';
import type { Model, GroupModel } from '../../../types';

// The lane helper is PURE — no React, no I/O. These cases pin the
// inference rule the client-side split folds with: the lane is derived
// by UNANIMITY over the group's CURRENT member rows, never trusted
// from write time, and never persisted.
//
// The rule is the FE's mirror of the server's request-time inference:
// a kindless ("") member is CHAT (the legacy normalisation), a
// decision member is DECISION, a mixed set is undecidable.

function makeModel(overrides: Partial<Model> = {}): Model {
  return {
    id: 'm1',
    name: 'M1',
    enabled: true,
    fallback_chain: [],
    ...overrides,
  };
}

function makeGroup(members: { model_id: string; weight: number }[]): GroupModel {
  return {
    ...makeModel({ id: 'g1', name: 'G1', kind: 'group' }),
    members,
    shadow_model_id: '',
  } as GroupModel;
}

// A kind index over a plain id→kind map.
function index(entries: Record<string, string>): Map<string, string> {
  return new Map(Object.entries(entries));
}

describe('groupLane — the client-side lane inference (decision-groups, phase 2)', () => {
  it('E1 — a group whose members are all chat or kindless is inferred as the chat lane', () => {
    const kindIndex = index({ 'chat-1': 'chat', 'chat-2': '' });
    const group = makeGroup([
      { model_id: 'chat-1', weight: 1 },
      { model_id: 'chat-2', weight: 1 },
    ]);
    expect(inferGroupLane(group, kindIndex)).toBe('chat');
    // The '' kind normalises to chat — never to decision.
    expect(normalizeKind('')).toBe('chat');
    expect(normalizeKind(undefined)).toBe('chat');
  });

  it('E2 — a group whose members are all decision-kind is inferred as the decision lane', () => {
    const kindIndex = index({ 'dec-1': 'decision', 'dec-2': 'decision' });
    const group = makeGroup([
      { model_id: 'dec-1', weight: 1 },
      { model_id: 'dec-2', weight: 1 },
    ]);
    expect(inferGroupLane(group, kindIndex)).toBe('decision');
  });

  it('E3 — a mixed chat + decision member set is undecidable (fail closed)', () => {
    const kindIndex = index({ 'chat-1': 'chat', 'dec-1': 'decision' });
    const group = makeGroup([
      { model_id: 'chat-1', weight: 1 },
      { model_id: 'dec-1', weight: 1 },
    ]);
    expect(inferGroupLane(group, kindIndex)).toBeNull();
  });

  it('E4 — a group with an unresolvable member id is undecidable (fail closed)', () => {
    const kindIndex = index({ 'dec-1': 'decision' });
    const group = makeGroup([
      { model_id: 'dec-1', weight: 1 },
      { model_id: 'ghost', weight: 1 },
    ]);
    expect(inferGroupLane(group, kindIndex)).toBeNull();
    // A group whose ONLY member is unresolvable is undecidable too.
    const orphan = makeGroup([{ model_id: 'ghost', weight: 1 }]);
    expect(inferGroupLane(orphan, kindIndex)).toBeNull();
  });

  it('E5 — a group with zero members is inferred as the chat lane (the legacy row stays visible)', () => {
    const kindIndex = index({ 'dec-1': 'decision' });
    const group = makeGroup([]);
    expect(inferGroupLane(group, kindIndex)).toBe('chat');
  });

  it('E6 — the split puts chat + unknown in the text list and decision only in the decision list', () => {
    const kindIndex = index({
      'chat-1': 'chat',
      'dec-1': 'decision',
      'dec-2': 'decision',
    });
    const chatGroup = makeGroup([{ model_id: 'chat-1', weight: 1 }]);
    const decisionGroup = { ...makeGroup([{ model_id: 'dec-1', weight: 1 }]), id: 'g2' } as GroupModel;
    const unknownGroup = {
      ...makeGroup([
        { model_id: 'dec-2', weight: 1 },
        { model_id: 'ghost', weight: 1 },
      ]),
      id: 'g3',
    } as GroupModel;

    const split = splitGroupsByLane([chatGroup, decisionGroup, unknownGroup], kindIndex);
    expect(split.chat.map((g) => g.id)).toEqual(['g1']);
    expect(split.decision.map((g) => g.id)).toEqual(['g2']);
    expect(split.unknown.map((g) => g.id)).toEqual(['g3']);
    // No row is invisible in both tabs: chat + unknown cover everything
    // that is not a decision group.
    expect([...split.chat, ...split.unknown].map((g) => g.id).sort()).toEqual(['g1', 'g3']);
  });

  it('E7 — the kind index prefers the narrow server-prefiltered lists over the unfiltered list', () => {
    // The unfiltered list is STALE for a model that was just created on
    // the decision lane (each hook refetches only its own slice), so
    // the narrow list must win.
    const stale = makeModel({ id: 'dec-1', name: 'Dec One' }); // no kind — the legacy default
    const fresh = makeModel({ id: 'dec-1', name: 'Dec One', kind: 'decision' });
    const kindIndex = buildKindIndex({ models: [stale], decisionModels: [fresh] });
    expect(kindIndex.get('dec-1')).toBe('decision');

    // The same precedence holds for the other narrow lists.
    const imgIndex = buildKindIndex({
      models: [makeModel({ id: 'img-1', name: 'Img One' })],
      imgGenModels: [makeModel({ id: 'img-1', name: 'Img One', kind: 'image-gen' })],
    });
    expect(imgIndex.get('img-1')).toBe('image-gen');

    const groupIndex = buildKindIndex({
      models: [makeModel({ id: 'grp-1', name: 'Grp One' })],
      groupModels: [makeModel({ id: 'grp-1', name: 'Grp One', kind: 'group' })],
    });
    expect(groupIndex.get('grp-1')).toBe('group');
  });
});
