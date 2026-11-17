import type { Model, GroupModel, GroupLane } from '../../types';

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
// (pkg/models/config.go EffectiveKind / pkg/models/group_lane.go
// MemberLane) — a chat-era row must never be read as a decision row.
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
// models.MemberLane:
//
//   '' / 'chat'   → chat     (the legacy "" kind IS chat)
//   'decision'    → decision
//   anything else → null     (image-gen / group belong to NO lane —
//                              they are not members in either lane)
//
// A null lane is "no lane evidence", NOT a vote for the other lane.
function memberLane(kind: string): 'chat' | 'decision' | null {
  if (kind === '' || kind === 'chat') return 'chat';
  if (kind === 'decision') return 'decision';
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
      index.set(m.id, kind ?? normalizeKind(m.kind));
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
// The lane is NEVER persisted and NEVER trusted from write time (the
// ruling's D-2): it is inferred, client-side, by unanimity over the
// member rows the FE can currently resolve.
//
//   - zero members            → 'chat' (a legacy / pre-homogeneity
//                               row: it must never vanish from the tab
//                               where it has always lived)
//   - every member is chat    → 'chat'
//   - every member is decision→ 'decision'
//   - a mixed set             → null (undecidable — fail closed)
//   - an unresolvable member  → null (undecidable — fail closed)
//   - a member of no lane     → null (image-gen / group are never
//                               members in either lane)
//
// null means "unknown lane": the row is not silently dropped — it
// stays visible in the TEXT tab, where the operator can repair it.
export function inferGroupLane(
  group: GroupModel | Model,
  kindIndex: Map<string, string>,
): GroupLane | null {
  const members = group.members ?? [];
  if (members.length === 0) return 'chat';

  let sawChat = false;
  let sawDecision = false;
  for (const member of members) {
    if (member.model_id === '') continue; // an unpicked row carries no lane evidence
    const raw = kindIndex.get(member.model_id);
    if (raw === undefined) return null; // unresolvable member id — undecidable
    const lane = memberLane(raw);
    if (lane === null) return null; // a member of no lane — undecidable
    if (lane === 'chat') sawChat = true;
    else sawDecision = true;
  }

  if (sawChat && sawDecision) return null; // mixed — undecidable
  if (sawDecision) return 'decision';
  if (sawChat) return 'chat';
  return null;
}

// The three buckets the parent hands to the two group surfaces.
export interface GroupLaneSplit {
  chat: GroupModel[];
  decision: GroupModel[];
  unknown: GroupModel[];
}

// splitGroupsByLane buckets the group list by the inferred lane.
//
// NO ROW IS EVER INVISIBLE IN BOTH TABS: the text lane receives
// chat + unknown, the decision lane receives decision only. A group
// the FE cannot classify stays in the text tab — next to its
// historical home, where the operator can repair it.
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
    const row = group as GroupModel;
    if (lane === 'decision') split.decision.push(row);
    else if (lane === 'chat') split.chat.push(row);
    else split.unknown.push(row);
  }
  return split;
}
