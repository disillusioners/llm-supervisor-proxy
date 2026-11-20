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
// The rule is the FE's mirror of the server's request-time fold
// (pkg/store/database/store_group_rewrite.go :369-385 over
// pkg/models/group_lane.go :95-101): a kindless ("") member is CHAT
// (the legacy normalisation), a decision member is DECISION, a member
// the FE cannot resolve (a vanished row) or a member of no lane
// (image-gen / group) carries NO lane evidence and is skipped, and the
// SURVIVING members decide — a mixed surviving set (or an empty one)
// is unknown.

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

// A kind OUTSIDE the FE's vocabulary — the server does not recognise
// it either, so its member carries NO lane evidence (models.MemberLane
// returns ok=false for any unknown kind). The FE's Model.kind is a
// closed union, so the value is declared as a plain string and cast at
// the single use site.
const legacyKind: string = 'legacy-kind';

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

  it('E4 — an unresolvable member id is skipped and the surviving members decide the lane', () => {
    const kindIndex = index({ 'dec-1': 'decision' });
    // A member id the FE cannot resolve is a VANISHED row: it carries
    // no lane evidence and is skipped (server :378-381), so the
    // surviving member decides.
    const group = makeGroup([
      { model_id: 'dec-1', weight: 1 },
      { model_id: 'ghost', weight: 1 },
    ]);
    expect(inferGroupLane(group, kindIndex)).toBe('decision');
    // A group whose ONLY member vanished has an EMPTY surviving set:
    // no lane is present at all, so the lane is unknown
    // (server models.UnanimousGroupLane :96-99).
    const orphan = makeGroup([{ model_id: 'ghost', weight: 1 }]);
    expect(inferGroupLane(orphan, kindIndex)).toBeNull();
  });

  it('E5 — a group with zero members has an empty surviving set, so its lane is unknown', () => {
    const kindIndex = index({ 'dec-1': 'decision' });
    const group = makeGroup([]);
    // Zero members ⇒ zero surviving lanes ⇒ unknown (server
    // models.UnanimousGroupLane :96-99) — NOT the chat lane. The row
    // stays VISIBLE all the same: the split files an unknown-lane
    // group into the TEXT tab.
    expect(inferGroupLane(group, kindIndex)).toBeNull();
    const split = splitGroupsByLane([group], kindIndex);
    expect(split.unknown.map((g) => g.id)).toEqual(['g1']);
    expect(split.chat).toEqual([]);
  });

  it('E6 — the split puts chat + unknown in the text list and decision only in the decision list', () => {
    const kindIndex = index({
      'chat-1': 'chat',
      'dec-1': 'decision',
    });
    const chatGroup = makeGroup([{ model_id: 'chat-1', weight: 1 }]);
    const decisionGroup = { ...makeGroup([{ model_id: 'dec-1', weight: 1 }]), id: 'g2' } as GroupModel;
    // A MIXED surviving set (chat + decision) is unknown: the server's
    // fold yields two valid lanes and no unanimity (server :96-99).
    const mixedGroup = {
      ...makeGroup([
        { model_id: 'chat-1', weight: 1 },
        { model_id: 'dec-1', weight: 1 },
      ]),
      id: 'g3',
    } as GroupModel;
    // An EMPTY surviving set (every member vanished) is unknown too.
    const vanishedGroup = {
      ...makeGroup([{ model_id: 'ghost', weight: 1 }]),
      id: 'g4',
    } as GroupModel;

    const split = splitGroupsByLane(
      [chatGroup, decisionGroup, mixedGroup, vanishedGroup],
      kindIndex,
    );
    expect(split.chat.map((g) => g.id)).toEqual(['g1']);
    expect(split.decision.map((g) => g.id)).toEqual(['g2']);
    expect(split.unknown.map((g) => g.id)).toEqual(['g3', 'g4']);
    // No row is invisible in both tabs: chat + unknown cover everything
    // that is not a decision group.
    expect([...split.chat, ...split.unknown].map((g) => g.id).sort()).toEqual([
      'g1',
      'g3',
      'g4',
    ]);
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

  it('E8 — a member whose kind belongs to no lane is ignored and the surviving members decide', () => {
    const kindIndex = index({
      'chat-1': 'chat',
      'dec-1': 'decision',
      'grp-1': 'group',
    });
    // A member that is itself a GROUP row belongs to NO lane — the
    // server's models.MemberLane returns ok=false for its kind and
    // models.GroupLanesOf ignores it (pkg/models/group_lane.go :53-62
    // and :72-94). The surviving members decide.
    // (The image-gen kind IS a lane since the imggen-groups commission
    // — it is pinned by E9, not here.)
    const withGroup = makeGroup([
      { model_id: 'dec-1', weight: 1 },
      { model_id: 'grp-1', weight: 1 },
    ]);
    expect(inferGroupLane(withGroup, kindIndex)).toBe('decision');
    // A group whose ONLY member is of no lane has an EMPTY surviving
    // set — no lane is present at all, so the lane is unknown (server
    // models.UnanimousGroupLane :103-109).
    const onlyNoLane = makeGroup([{ model_id: 'grp-1', weight: 1 }]);
    expect(inferGroupLane(onlyNoLane, kindIndex)).toBeNull();

    // A kind OUTSIDE the vocabulary is the same case: the server's
    // models.MemberLane returns ok=false for ANY kind it does not
    // recognise and models.GroupLanesOf ignores it (pkg/models/group_lane.go
    // :46-55 and :67-71) — the member carries NO lane evidence, so the
    // surviving members decide.
    const legacyIndex = index({ 'chat-1': 'chat', 'legacy-1': legacyKind });
    const withLegacyKind = makeGroup([
      { model_id: 'chat-1', weight: 1 },
      { model_id: 'legacy-1', weight: 1 },
    ]);
    expect(inferGroupLane(withLegacyKind, legacyIndex)).toBe('chat');
    // A group whose ONLY member has an out-of-vocabulary kind has an
    // EMPTY surviving set — no lane is present at all, so the lane is
    // unknown (server models.UnanimousGroupLane :96-99).
    const onlyLegacyKind = makeGroup([{ model_id: 'legacy-1', weight: 1 }]);
    expect(inferGroupLane(onlyLegacyKind, legacyIndex)).toBeNull();

    // The INDEX must carry the RAW kind: the FE normalises a kind only
    // where the server normalises it (the legacy "" kind IS chat) —
    // normalising an UNKNOWN kind into a chat vote would make the FE
    // answer "chat" for a member the server ignores. The server reads
    // the member row's kind verbatim (store_group_rewrite.go :378-384
    // hands the raw kind to models.MemberLane), so the index must too.
    const built = buildKindIndex({
      models: [
        makeModel({ id: 'dec-1', name: 'Dec One', kind: 'decision' }),
        makeModel({ id: 'legacy-1', name: 'Legacy One', kind: legacyKind as Model['kind'] }),
      ],
    });
    expect(built.get('legacy-1')).toBe(legacyKind);
    // The fold over the BUILT index ignores the out-of-vocabulary member
    // instead of counting it as a chat vote (a chat vote here would make
    // the surviving set MIXED and the lane unknown — the FE would answer
    // "unknown" where the server answers "decision").
    const builtMixed = makeGroup([
      { model_id: 'dec-1', weight: 1 },
      { model_id: 'legacy-1', weight: 1 },
    ]);
    expect(inferGroupLane(builtMixed, built)).toBe('decision');
    const builtOnlyLegacy = makeGroup([{ model_id: 'legacy-1', weight: 1 }]);
    expect(inferGroupLane(builtOnlyLegacy, built)).toBeNull();
  });

  it('E9 — an image-gen member votes for the image-gen lane; a mixed image-gen set is undecidable', () => {
    const kindIndex = index({
      'chat-1': 'chat',
      'dec-1': 'decision',
      'img-1': 'image-gen',
    });
    // The image-gen kind IS a lane (the imggen-groups commission — the
    // FE twin of the server's models.MemberLane image-gen arm, ADR-19:
    // the two folds must not ship different answers). A group of
    // image-gen members is an IMAGE-GEN group, exactly as the server
    // answers for the same members.
    const imgGroup = makeGroup([{ model_id: 'img-1', weight: 1 }]);
    expect(inferGroupLane(imgGroup, kindIndex)).toBe('image-gen');

    // A MIXED surviving set (chat + image-gen, decision + image-gen) is
    // undecidable: the server's fold yields two valid lanes and no
    // unanimity (server models.UnanimousGroupLane :103-109 ⇒ ok=false).
    // The FE must answer null for EXACTLY the inputs the server
    // refuses — a group that mixes lanes is unrepresentable at write
    // time and fails closed at request time.
    const chatAndImg = makeGroup([
      { model_id: 'chat-1', weight: 1 },
      { model_id: 'img-1', weight: 1 },
    ]);
    expect(inferGroupLane(chatAndImg, kindIndex)).toBeNull();
    const decAndImg = makeGroup([
      { model_id: 'dec-1', weight: 1 },
      { model_id: 'img-1', weight: 1 },
    ]);
    expect(inferGroupLane(decAndImg, kindIndex)).toBeNull();
  });
});
