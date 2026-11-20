import type { Model, GroupModel, GroupLane } from '../../types';

// ─── READ FIRST — the legacy-kind normalisation is NOT the lane fold ────────
// WARNING — normalizeKind (below) is the LEGACY-KIND normalisation, NOT the
// lane fold. It must NOT be used to decide a member's lane: it maps an
// unrecognised kind to the chat default, while the server's
// models.MemberLane (pkg/models/group_lane.go :53-62) returns ok=false
// for such a kind and models.GroupLanesOf (:72-94) IGNORES the member
// (it is unservable, not a lane vote). The lane fold therefore decides
// FROM THE RAW KIND — buildKindIndex stores the raw kind for exactly
// this reason, and memberLane (below) applies the server's rule.
//
// normalizeKind is exported for the E1 pin ONLY — zero production callers;
// deleting it requires deleting the two E1 assertions in the same commit;
// it must NEVER decide a lane.
// ─────────────────────────────────────────────────────────────────────────────

// The lane vocabulary is owned by types.ts (GroupLane); it is re-exported
// here so the group surfaces import the type and the helpers from ONE
// module (the same reason the helper lives beside the components that
// consume it).
export type { GroupLane };

// The model-kind vocabulary the FE knows about — mirrors the FE's
// Model.kind union. The helper is PURE: no React, no I/O, no hooks —
// every function is a deterministic fold over plain data.
export type ModelKind = 'chat' | 'image-gen' | 'decision' | 'group';

// normalizeKind applies the legacy-kind normalisation: an absent /
// empty kind IS chat. It mirrors the server's normalisation
// (pkg/models/group_lane.go:111 EffectiveKind / pkg/models/group_lane.go
// MemberLane) — a chat-era row must never be read as a decision row.
//
// The invariant that keeps this helper OUT of the lane fold is stated in
// the FILE HEADER — read it before using this function.
//
// The FE's typed data cannot produce a kind outside the vocabulary
// (Model.kind is a closed union), so the fallback branch is defensive
// only: an unrecognised kind normalises to the legacy chat default.
export function normalizeKind(kind?: string): ModelKind {
  if (kind === undefined || kind === '') return 'chat';
  if (kind === 'chat' || kind === 'image-gen' || kind === 'decision' || kind === 'group') {
    return kind;
  }
  return 'chat';
}

// The lane a member kind votes for — the FE's mirror of the server's
// models.MemberLane (pkg/models/group_lane.go :53-62):
//
//   '' / 'chat'   → chat     (the legacy "" kind IS chat)
//   'decision'    → decision
//   'image-gen'   → image-gen (the imggen-groups commission: the
//                              image-gen kind is a LANE of its own —
//                              the lane value mirrors the server
//                              constant literally)
//   anything else → null     (a group row / an unknown kind belongs
//                              to NO lane — it is not a member in any
//                              lane)
//
// A null lane is "no lane evidence", NOT a vote for the other lane:
// the server ignores such a member (models.GroupLanesOf :72-94).
function memberLane(kind: string): 'chat' | 'decision' | 'image-gen' | null {
  if (kind === '' || kind === 'chat') return 'chat';
  if (kind === 'decision') return 'decision';
  if (kind === 'image-gen') return 'image-gen';
  return null;
}

// The model lists the kind index is built from. The NARROW lists are
// the server-prefiltered ones (useModels / useImgGenModels /
// useDecisionModels / useGroupModels) — each hook fetches its own
// slice, so the unfiltered `models` array can be STALE for a model
// that was just created on another lane.
export interface KindIndexSources {
  models?: Model[];
  decisionModels?: Model[];
  groupModels?: Model[];
  imgGenModels?: Model[];
}

// buildKindIndex maps model_id → kind across the union of the supplied
// lists.
//
// The KIND IS THE RAW KIND. The lane fold decides a member's lane FROM
// the raw kind (memberLane below is the mirror of the server's
// models.MemberLane), so the index must NOT normalise a kind before
// the fold: the server reads the member row's kind verbatim
// (pkg/store/database/store_group_rewrite.go :378-384 hands the raw
// kind to models.MemberLane), and a kind it does not recognise yields
// NO lane evidence — it is ignored, never a chat vote.
//
// Precedence: the NARROW server-prefiltered lists win over the
// unfiltered one. Each hook's mutation deletes the shared 'models'
// cache key but only refetches ITS OWN list, so a just-created
// decision model can be missing from `models` while it IS present in
// `decisionModels` — indexing the unfiltered list alone would leave
// that member unresolvable.
//
// The narrow lists are applied LAST (models → image-gen → group →
// decision), so the narrowest (decision) wins any collision.
export function buildKindIndex(sources: KindIndexSources): Map<string, string> {
  const index = new Map<string, string>();
  const apply = (list: Model[] | undefined, kind: string | undefined) => {
    for (const m of list ?? []) {
      // The narrow lists carry their own literal kind (the hook that
      // fetched them is server-prefiltered by kind); the unfiltered list
      // carries the row's OWN kind — kept RAW, because the lane fold
      // reads the raw kind (an unknown kind must yield no lane
      // evidence, exactly as models.MemberLane does on the server).
      index.set(m.id, kind ?? (m.kind ?? ''));
    }
  };
  apply(sources.models, undefined);
  apply(sources.imgGenModels, 'image-gen');
  apply(sources.groupModels, 'group');
  apply(sources.decisionModels, 'decision');
  return index;
}

// inferGroupLane derives a group's LANE from its CURRENT member rows.
//
// The SERVER's fold is the runtime truth — it is what actually
// decides which lane serves a group — so this function mirrors it
// function for function, case for case. The FE's job is to display
// what the server will actually do.
//
// The server's fold (pkg/store/database/store_group_rewrite.go
// inferGroupLane :369-385) folds over the group's member rows and
// hands the kinds to models.UnanimousGroupLane
// (pkg/models/group_lane.go :103-109):
//
//   - a member with an EMPTY id → skipped, no lane evidence
//     (server :375-377)
//   - a member id that resolves to NO row (a VANISHED member) →
//     skipped, no lane evidence (server :378-381; the
//     collection-shaped twin pkg/models/group_lane.go :147-150)
//   - a member whose kind belongs to NO lane (a group row, an unknown
//     kind) → skipped, no lane evidence: models.MemberLane :53-62
//     returns ok=false for such a kind and models.GroupLanesOf :72-94
//     ignores it (an unservable member is not a lane vote)
//   - the SURVIVING members then vote: exactly ONE lane → that
//     lane (server :103-109); ZERO surviving lanes (an EMPTY surviving
//     set) or TWO lanes (a MIXED surviving set) → unknown.
//
// null means "unknown lane": the row is not silently dropped — it
// stays visible in the TEXT tab, where the operator can repair it.
// The server fails such a group CLOSED at pick time
// (pkg/store/database/store_group_rewrite.go :162-166); the FE
// mirrors the FOLD, not that consequence.
export function inferGroupLane(
  group: GroupModel | Model,
  kindIndex: Map<string, string>,
): GroupLane | null {
  const members = group.members ?? [];
  const lanes = new Set<'chat' | 'decision' | 'image-gen'>();

  for (const member of members) {
    if (member.model_id === '') continue; // server :375-377 — an unpicked row carries no lane evidence
    const raw = kindIndex.get(member.model_id);
    if (raw === undefined) continue; // server :378-381 — a vanished member carries no lane evidence
    const lane = memberLane(raw);
    if (lane === null) continue; // server :72-94 — a member of no lane is ignored, not a vote
    lanes.add(lane);
  }

  if (lanes.size !== 1) return null; // server :103-109 — empty (0) or mixed (2+) surviving set → unknown
  return lanes.has('decision')
    ? 'decision'
    : lanes.has('image-gen')
      ? 'image-gen'
      : 'chat';
}

// The three buckets the parent hands to the two group surfaces.
//
// The buckets are typed as the ACTUAL INPUT (Model[]), not as
// GroupModel[]: splitGroupsByLane receives Model[] and Model.kind is
// OPTIONAL, so a GroupModel[] bucket would assert a kind the input never
// guaranteed (the old `as GroupModel` cast). Both consumers already take
// Model[] (SettingsPage.tsx hands GroupModelsTab.models the chat+unknown
// spread and DecisionModelsContainer.groups the decision bucket).
export interface GroupLaneSplit {
  chat: Model[];
  decision: Model[];
  unknown: Model[];
}

// splitGroupsByLane buckets the group list by the inferred lane.
//
// NO ROW IS EVER INVISIBLE IN BOTH TABS: the text lane receives
// chat + unknown, the decision lane receives decision only. A group
// the FE cannot classify stays in the text tab — next to its
// historical home, where the operator can repair it.
//
// "Unknown" is the FE's mirror of the server's UNRESOLVED lane
// (pkg/store/database/store_group_rewrite.go :162-166 — the server
// fails such a group closed at pick time; the FE mirrors the fold,
// not that consequence).
//
// The split is DISPLAY-ONLY. The server stays authoritative on every
// write (the widened group validators decide legality), and the
// client's classification never changes what is persisted.
export function splitGroupsByLane(
  groups: Model[],
  kindIndex: Map<string, string>,
): GroupLaneSplit {
  const split: GroupLaneSplit = { chat: [], decision: [], unknown: [] };
  for (const group of groups) {
    const lane = inferGroupLane(group, kindIndex);
    // The row is a Model — Model.kind is OPTIONAL, so the old
    // `as GroupModel` assertion claimed a kind the input never
    // guaranteed. The alias is kept (typed as the ACTUAL input type)
    // so the emitted bundle stays byte-identical: the change is
    // type-level only, with zero runtime effect.
    const row: Model = group;
    if (lane === 'decision') split.decision.push(row);
    else if (lane === 'chat') split.chat.push(row);
    // The 'image-gen' lane has NO bucket yet: the ImgGen container and
    // its Group sub-tab are Phase-3 work, so an image-gen group falls
    // into `unknown` — it stays VISIBLE in the text tab (the
    // single-home invariant: no row is ever invisible) until Phase 3
    // gives the lane its own home. The FOLD above already answers
    // 'image-gen' (the parity with the server is in inferGroupLane);
    // only the DISPLAY bucket is still missing.
    else split.unknown.push(row);
  }
  return split;
}
