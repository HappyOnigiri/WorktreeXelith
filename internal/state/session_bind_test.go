package state

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func createAgentBindSession(t *testing.T, store *Store, id, kind string) {
	t.Helper()
	session := Session{ID: id, SlotID: id, State: "ACTIVE", AgentKind: kind, TokenHash: HashToken(id)}
	if _, err := store.CreateSlotSession(context.Background(), Slot{
		ID: id, State: "LEASED", RootID: testRootID, RelPath: filepath.Join("_unbound", id),
	}, nil, session, ""); err != nil {
		t.Fatal(err)
	}
}

func bindAgentSessionForTest(t *testing.T, store *Store, id, agentID string) {
	t.Helper()
	if err := store.BindAgentSession(context.Background(), id, agentID); err != nil {
		t.Fatal(err)
	}
}

func TestBindAgentSessionReplacesVerifiedForkMapping(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := context.Background()
	createAgentBindSession(t, store, "fork-root", "codex")
	bindAgentSessionForTest(t, store, "fork-root", "native-parent")

	primary, err := store.BindAgentSessionFromHook(ctx, "fork-root", "native-child", "fork", "native-parent")
	if err != nil || !primary {
		t.Fatalf("replace agent session mapping: primary=%v err=%v", primary, err)
	}
	got, err := store.FindByAgentSession(ctx, "codex", "native-child")
	if err != nil || got.ID != "fork-root" {
		t.Fatalf("new mapping=%+v err=%v", got, err)
	}
	if _, err := store.FindByAgentSession(ctx, "codex", "native-parent"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old mapping still exists: err=%v", err)
	}
}

func TestBindAgentSessionNormalBindIsIdempotent(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := context.Background()
	createAgentBindSession(t, store, "idempotent-root", "codex")
	bindAgentSessionForTest(t, store, "idempotent-root", "native")
	if err := store.BindAgentSession(ctx, "idempotent-root", "native"); err != nil {
		t.Fatalf("same native ID bind: %v", err)
	}
}

func TestBindAgentSessionFromHookIgnoresAuxiliaryCodexStarts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, source := range []string{"startup", "fork"} {
		t.Run(source, func(t *testing.T) {
			store := openTestStore(t)
			id := "auxiliary-" + source
			createAgentBindSession(t, store, id, "codex")
			bindAgentSessionForTest(t, store, id, "native-primary")

			primary, err := store.BindAgentSessionFromHook(ctx, id, "native-btw", source)
			if err != nil || primary {
				t.Fatalf("auxiliary bind: primary=%v err=%v", primary, err)
			}
			got, err := store.SessionByID(ctx, id)
			if err != nil || got.AgentSessionID != "native-primary" || got.State != "ACTIVE" {
				t.Fatalf("auxiliary bind changed session: %+v err=%v", got, err)
			}
		})
	}
}

func TestBindAgentSessionFromHookKeepsStrictConflicts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, test := range []struct {
		name, kind, source string
	}{
		{name: "Codex resume", kind: "codex", source: "resume"},
		{name: "Codex clear", kind: "codex", source: "clear"},
		{name: "Codex unknown", kind: "codex", source: "future"},
		{name: "Claude startup", kind: "claude", source: "startup"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			session := Session{ID: test.name, SlotID: test.name, State: "ACTIVE", AgentKind: test.kind, TokenHash: HashToken(test.name)}
			if _, err := store.CreateSlotSession(ctx, Slot{ID: test.name, State: "LEASED", RootID: testRootID, RelPath: filepath.Join("_unbound", test.name)}, nil, session, ""); err != nil {
				t.Fatal(err)
			}
			bindAgentSessionForTest(t, store, test.name, "native-primary")
			if _, err := store.BindAgentSessionFromHook(ctx, test.name, "native-other", test.source); err == nil {
				t.Fatal("conflicting bind was accepted")
			}
		})
	}
	for _, source := range []string{"startup", "fork"} {
		t.Run("child Codex "+source, func(t *testing.T) {
			store := openTestStore(t)
			parentID := "parent-" + source
			childID := "child-" + source
			createAgentBindSession(t, store, parentID, "codex")
			child := Session{ID: childID, SlotID: childID, ParentSessionID: parentID, State: "ACTIVE", AgentKind: "codex", TokenHash: HashToken(childID)}
			if _, err := store.CreateSlotSession(ctx, Slot{ID: childID, State: "LEASED", RootID: testRootID, RelPath: filepath.Join("_unbound", childID)}, nil, child, ""); err != nil {
				t.Fatal(err)
			}
			bindAgentSessionForTest(t, store, childID, "native-primary")
			if _, err := store.BindAgentSessionFromHook(ctx, childID, "native-other", source); err == nil {
				t.Fatal("child session accepted an auxiliary bind")
			}
		})
	}
}

func TestBindAgentSessionRejectsForkWhenSourceChanged(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := context.Background()
	createAgentBindSession(t, store, "changed-root", "codex")
	bindAgentSessionForTest(t, store, "changed-root", "native-current")

	if err := store.BindAgentSession(ctx, "changed-root", "native-child", "native-old"); err == nil {
		t.Fatal("fork replacement accepted a stale source mapping")
	}
	got, err := store.SessionByID(ctx, "changed-root")
	if err != nil || got.AgentSessionID != "native-current" {
		t.Fatalf("stale replacement changed mapping: %+v err=%v", got, err)
	}
}

func TestBindAgentSessionRejectsForkWhenTargetIsOwned(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := context.Background()
	createAgentBindSession(t, store, "target-root", "codex")
	bindAgentSessionForTest(t, store, "target-root", "native-parent")
	createAgentBindSession(t, store, "other-root", "codex")
	bindAgentSessionForTest(t, store, "other-root", "native-child")

	if err := store.BindAgentSession(ctx, "target-root", "native-child", "native-parent"); err == nil {
		t.Fatal("fork replacement stole another session's native ID")
	}
	got, err := store.SessionByID(ctx, "target-root")
	if err != nil || got.AgentSessionID != "native-parent" {
		t.Fatalf("target mapping changed after conflict: %+v err=%v", got, err)
	}
}

func TestBindAgentSessionRejectsDelayedForkAfterReplacement(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := context.Background()
	createAgentBindSession(t, store, "retry-root", "codex")
	bindAgentSessionForTest(t, store, "retry-root", "native-parent")

	if err := store.BindAgentSession(ctx, "retry-root", "native-child", "native-parent"); err != nil {
		t.Fatalf("first replacement: %v", err)
	}
	if err := store.BindAgentSession(ctx, "retry-root", "native-child", "native-parent"); err == nil {
		t.Fatal("delayed fork replacement was accepted after the old mapping changed")
	}
}

func TestClaudeClearRebindsNativeSession(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	seedWorkspace(t, store)
	ctx := context.Background()
	session := Session{ID: "claude", SlotID: "claude", WorkspaceID: "workspace", State: "ACTIVE", AgentKind: "claude", TokenHash: HashToken("claude")}
	if _, err := store.CreateSlotSession(ctx, Slot{
		ID: session.SlotID, WorkspaceID: session.WorkspaceID, RootID: testRootID, RelPath: "workspace/claude", State: "LEASED",
	}, nil, session, ""); err != nil {
		t.Fatal(err)
	}
	for _, event := range []struct{ source, nativeID string }{
		{source: "startup", nativeID: "native-before-clear"},
		{source: "clear", nativeID: "native-after-clear"},
		{source: "clear", nativeID: "native-after-clear"},
		{source: "compact", nativeID: "native-after-clear"},
		{source: "resume", nativeID: "native-after-clear"},
	} {
		primary, err := store.BindAgentSessionFromHook(ctx, session.ID, event.nativeID, event.source)
		if err != nil || !primary {
			t.Fatalf("Claude %s: primary=%v err=%v", event.source, primary, err)
		}
	}
	got, err := store.FindByAgentSession(ctx, "claude", "native-after-clear")
	if err != nil || got.ID != session.ID || got.State != "ACTIVE" {
		t.Fatalf("new native mapping: session=%+v err=%v", got, err)
	}
	if _, err := store.FindByAgentSession(ctx, "claude", "native-before-clear"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old native mapping still exists: err=%v", err)
	}
	job, changed, _, matched, err := store.ReleaseWithAgentSessionOutcome(ctx, session.ID, session.WorkspaceID, session.SlotID, "native-before-clear")
	if err != nil || matched || changed || job.ID != "" {
		t.Fatalf("old SessionEnd: matched=%v changed=%v job=%+v err=%v", matched, changed, job, err)
	}
	job, changed, _, matched, err = store.ReleaseWithAgentSessionOutcome(ctx, session.ID, session.WorkspaceID, session.SlotID, "native-after-clear")
	if err != nil || !matched || !changed || job.Kind != "SNAPSHOT" {
		t.Fatalf("new SessionEnd: matched=%v changed=%v job=%+v err=%v", matched, changed, job, err)
	}
}

func TestClaudeClearRejectsOwnedNativeSession(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, currentID string }{
		{name: "bound child", currentID: "native-before-clear"},
		{name: "unbound child"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			ctx := context.Background()
			createAgentBindSession(t, store, "parent", "claude")
			bindAgentSessionForTest(t, store, "parent", "native-owned")
			child := Session{ID: "child", SlotID: "child", ParentSessionID: "parent", State: "ACTIVE", AgentKind: "claude", TokenHash: HashToken("child")}
			if _, err := store.CreateSlotSession(ctx, Slot{
				ID: child.SlotID, RootID: testRootID, RelPath: "_unbound/child", State: "LEASED",
			}, nil, child, ""); err != nil {
				t.Fatal(err)
			}
			if test.currentID != "" {
				bindAgentSessionForTest(t, store, child.ID, test.currentID)
			}
			if _, err := store.BindAgentSessionFromHook(ctx, child.ID, "native-owned", "clear"); err == nil {
				t.Fatal("clear accepted a native ID owned by another wx session")
			}
			got, err := store.SessionByID(ctx, child.ID)
			if err != nil || got.AgentSessionID != test.currentID {
				t.Fatalf("clear conflict changed child: session=%+v err=%v", got, err)
			}
			owner, err := store.FindByAgentSession(ctx, "claude", "native-owned")
			if err != nil || owner.ID != "parent" {
				t.Fatalf("clear conflict changed ownership: session=%+v err=%v", owner, err)
			}
		})
	}
}

func TestClaudeResumeRejectsOwnedOrReservedNativeSession(t *testing.T) {
	t.Parallel()
	for _, claim := range []string{"owned", "reserved"} {
		t.Run(claim, func(t *testing.T) {
			store := openTestStore(t)
			ctx := context.Background()
			createAgentBindSession(t, store, "parent", "claude")
			ownerID := "parent"
			if claim == "owned" {
				bindAgentSessionForTest(t, store, ownerID, "native-target")
			} else {
				ownerID = "restoring"
				other := Session{
					ID: ownerID, SlotID: ownerID, ParentSessionID: "parent", State: "RESTORING",
					AgentKind: "claude", PendingAgentSessionID: "native-target", TokenHash: HashToken(ownerID),
				}
				if _, err := store.CreateSlotSession(ctx, Slot{
					ID: ownerID, RootID: testRootID,
					RelPath: "_unbound/restoring", State: "RESTORING",
				}, nil, other, ""); err != nil {
					t.Fatal(err)
				}
			}
			child := Session{ID: "child", SlotID: "child", ParentSessionID: "parent", State: "ACTIVE", AgentKind: "claude", TokenHash: HashToken("child")}
			if _, err := store.CreateSlotSession(ctx, Slot{
				ID: child.SlotID, RootID: testRootID,
				RelPath: "_unbound/child", State: "LEASED",
			}, nil, child, ""); err != nil {
				t.Fatal(err)
			}
			bindAgentSessionForTest(t, store, child.ID, "native-current")

			if _, err := store.BindAgentSessionFromHook(ctx, child.ID, "native-target", "resume"); err == nil {
				t.Fatalf("resume accepted a %s native session", claim)
			}
			got, err := store.SessionByID(ctx, child.ID)
			if err != nil || got.AgentSessionID != "native-current" || got.State != "ACTIVE" {
				t.Fatalf("rejected resume changed the lease: session=%+v err=%v", got, err)
			}
			owner, err := store.SessionByID(ctx, ownerID)
			if err != nil {
				t.Fatal(err)
			}
			if claim == "owned" && owner.AgentSessionID != "native-target" {
				t.Fatalf("rejected resume changed ownership: session=%+v", owner)
			}
			if claim == "reserved" && (owner.State != "RESTORING" || owner.PendingAgentSessionID != "native-target" || owner.AgentSessionID != "") {
				t.Fatalf("rejected resume changed the reservation: session=%+v", owner)
			}
		})
	}
}

func TestClaudeResumeRejectsSwitchAfterReleaseStarts(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := context.Background()
	createAgentBindSession(t, store, "claude", "claude")
	bindAgentSessionForTest(t, store, "claude", "native-current")
	if _, changed, _, err := store.ReleaseWithOutcome(ctx, "claude", "", "claude"); err != nil || !changed {
		t.Fatalf("start release: changed=%v err=%v", changed, err)
	}
	if _, err := store.BindAgentSessionFromHook(ctx, "claude", "native-target", "resume"); err == nil {
		t.Fatal("resume changed a releasing session")
	}
	got, err := store.SessionByID(ctx, "claude")
	if err != nil || got.AgentSessionID != "native-current" || got.State != "RELEASING" {
		t.Fatalf("rejected resume changed the releasing session: session=%+v err=%v", got, err)
	}
}
