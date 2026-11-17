import { useState, useEffect, useCallback } from 'preact/hooks';
import type { CredentialGroup, CredentialRef, Model } from '../../types';
import {
  ApiError,
  CREDENTIAL_GROUP_IN_USE_MESSAGE,
  deleteCredentialGroup,
  getCredentialGroups,
} from '../../hooks/useApi';
import { CredentialsTab } from './CredentialsTab';
import { CredentialGroupsTab } from './CredentialGroupsTab';

// credential-groups commission (2026-11-16) — the credentials-area
// container.
//
// The container owns:
//   * the vertical left sub-nav (Credentials | Credential Groups) —
//     a SIBLING of the top-level SettingsPage tab bar, never a new
//     top-level tab (the bar stays at 11 tabs);
//   * the groups fetch (ONE owner of the `groups` state — the badge map
//     and the "used by" map both derive from this UNCACHED list, never
//     from the 15s-TTL models cache);
//   * its OWN status state. SettingsPage has no `status` value to hand
//     down — only `setStatusWrapper` — so the container declares the
//     status itself and forwards it to the children.
//
// The container has NO page-level heading of its own: each child panel
// owns its heading (that is what keeps CredentialsTab as close to
// byte-unchanged as the badge requirement allows).

interface CredentialsContainerProps {
  models: Model[];
  onUpdateModel: (id: string, updates: Partial<Model>) => Promise<void>;
  setStatusWrapper: (status: { type: 'success' | 'error'; message: string; restartRequired?: boolean } | null) => void;
}

type ContainerStatus = { type: 'success' | 'error'; message: string };

// How many times the inline-then-delete flow re-reads the server's
// `used_by` and re-attempts the delete before it gives up. The SERVER
// is the authority on which models still reference the group.
const MAX_INLINE_DELETE_ATTEMPTS = 3;

/**
 * Replace a group ref with the group's member list. Each inlined member
 * inherits the group ref's weight (the effective routing weight is
 * preserved) and the positions are reindexed.
 */
function inlineGroupRef(refs: CredentialRef[], group: CredentialGroup): CredentialRef[] {
  const next: CredentialRef[] = [];
  for (const ref of refs) {
    if (ref.group_id === group.id) {
      for (const memberId of group.members || []) {
        next.push({
          credential_id: memberId,
          group_id: undefined,
          weight: ref.weight,
          position: next.length,
        });
      }
    } else {
      next.push({
        // F4: `?? ''` — NOT `|| undefined` (a group ref carries
        // credential_id: "" and CredentialRef.credential_id is a
        // required string).
        credential_id: ref.credential_id ?? '',
        group_id: ref.group_id,
        weight: ref.weight,
        position: next.length,
      });
    }
  }
  return next;
}

export function CredentialsContainer({
  models,
  onUpdateModel,
  setStatusWrapper,
}: CredentialsContainerProps) {
  const [child, setChild] = useState<'credentials' | 'groups'>('credentials');
  const [groups, setGroups] = useState<CredentialGroup[]>([]);

  // F1 — the container's OWN status state. A raw useState setter cannot
  // call the passed setStatusWrapper, so this wrapper updates the local
  // state AND forwards to SettingsPage's toast channel.
  const [status, setLocalStatus] = useState<ContainerStatus | null>(null);
  const setStatus = useCallback(
    (next: ContainerStatus | null) => {
      setLocalStatus(next);
      setStatusWrapper(next);
    },
    [setStatusWrapper],
  );

  // F3 — ONE owner of the groups state. Uncached (mirrors
  // getCredentials). Returns the fresh list so the delete-block flow can
  // read the blocked group's server-side `used_by` from the RETURNED
  // array — never from the error text.
  const refreshGroups = useCallback(async (): Promise<CredentialGroup[]> => {
    const data = await getCredentialGroups();
    const list = Array.isArray(data) ? data : [];
    setGroups(list);
    return list;
  }, []);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        await refreshGroups();
      } catch (e) {
        if (cancelled) return;
        setStatus({
          type: 'error',
          message: e instanceof Error ? e.message : 'Failed to fetch credential groups',
        });
      }
    };
    void load();
    return () => {
      cancelled = true;
    };
  }, [refreshGroups]);

  // credential id → group names (the badge map CredentialsTab renders).
  const groupMemberships: Record<string, string[]> = {};
  for (const group of groups) {
    for (const memberId of group.members || []) {
      const existing = groupMemberships[memberId];
      if (existing) {
        existing.push(group.name);
      } else {
        groupMemberships[memberId] = [group.name];
      }
    }
  }

  // group id → referencing model ids (server-side `used_by`).
  const usedBy: Record<string, string[]> = {};
  for (const group of groups) {
    if (group.used_by) {
      usedBy[group.id] = group.used_by;
    }
  }

  // The 409 recovery action: inline the group's members into every
  // referencing model (through the existing model-save flow), then
  // re-attempt the delete until the group is gone. No backend endpoint
  // exists for this — it is FE-driven.
  const handleInlineAndDelete = useCallback(
    async (group: CredentialGroup): Promise<void> => {
      for (let attempt = 0; attempt < MAX_INLINE_DELETE_ATTEMPTS; attempt++) {
        const fresh = await refreshGroups();
        const current = fresh.find((g) => g.id === group.id);
        if (!current) {
          // Already gone (deleted by an earlier attempt or elsewhere).
          setStatus({ type: 'success', message: 'Credential group deleted successfully' });
          return;
        }
        for (const modelId of current.used_by || []) {
          const model = models.find((m) => m.id === modelId);
          if (!model) continue;
          const next = inlineGroupRef(model.credentials || [], current);
          await onUpdateModel(model.id, { credentials: next });
        }
        try {
          await deleteCredentialGroup(current.id);
          await refreshGroups();
          setStatus({ type: 'success', message: 'Credential group deleted successfully' });
          return;
        } catch (e) {
          if (!(e instanceof ApiError && e.status === 409)) throw e;
          // Still referenced (the model save has not landed yet) — loop:
          // re-read the server's `used_by` and retry.
        }
      }
      setStatus({ type: 'error', message: CREDENTIAL_GROUP_IN_USE_MESSAGE });
    },
    [models, onUpdateModel, refreshGroups, setStatus],
  );

  return (
    <div class="flex gap-4" data-testid="credential-groups-container">
      {/* The sub-nav is a SIBLING of the top-level tab bar — it is not
          inside `div.flex.overflow-x-auto`, and it uses no
          role=tablist/tab/tabpanel and no aria-selected. `aria-current`
          marks the ACTIVE button only (the inactive one omits it). */}
      <nav
        class="shrink-0 w-40 flex flex-col gap-1 border-r border-gray-700 pr-2"
        data-testid="credential-groups-nav"
        aria-label="Credentials sections"
      >
        <button
          type="button"
          class={
            child === 'credentials'
              ? 'px-3 py-2 text-left font-medium transition-colors whitespace-nowrap border-l-2 text-blue-400 border-l-blue-400'
              : 'px-3 py-2 text-left font-medium transition-colors whitespace-nowrap border-l-2 text-gray-400 hover:text-white border-l-transparent'
          }
          data-testid="credential-groups-tab-credentials"
          aria-label="Credentials"
          aria-current={child === 'credentials' ? 'true' : undefined}
          onClick={() => setChild('credentials')}
        >
          Credentials
        </button>
        <button
          type="button"
          class={
            child === 'groups'
              ? 'px-3 py-2 text-left font-medium transition-colors whitespace-nowrap border-l-2 text-blue-400 border-l-blue-400'
              : 'px-3 py-2 text-left font-medium transition-colors whitespace-nowrap border-l-2 text-gray-400 hover:text-white border-l-transparent'
          }
          data-testid="credential-groups-tab-groups"
          aria-label="Credential Groups"
          aria-current={child === 'groups' ? 'true' : undefined}
          onClick={() => setChild('groups')}
        >
          Credential Groups
        </button>
      </nav>
      <div class="flex-1 min-w-0" data-testid="credential-groups-panel">
        {child === 'credentials' ? (
          <CredentialsTab
            status={status}
            setStatus={setStatus}
            groupMemberships={groupMemberships}
          />
        ) : (
          <CredentialGroupsTab
            groups={groups}
            models={models}
            usedBy={usedBy}
            onInlineAndDelete={handleInlineAndDelete}
            onRefreshGroups={refreshGroups}
            status={status}
            setStatus={setStatus}
          />
        )}
      </div>
    </div>
  );
}
