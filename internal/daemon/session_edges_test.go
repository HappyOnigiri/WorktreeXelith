package daemon

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/HappyOnigiri/WorktreeX/internal/state"
)

func TestSessionEndWaitsForForegroundClientExit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, err := openTestStoreAtPath(t, filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.CreateSlotSession(ctx, storeSlotAt(t, store, root, "", "live", filepath.Join(root, "live"), 0, "LEASED"), nil, state.Session{ID: "live", SlotID: "live", State: "ACTIVE", AgentKind: "codex", ClientPID: os.Getpid(), TokenHash: state.HashToken("token")}, ""); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{store: store, jobQueue: newJobQueue(1), log: slog.New(slog.NewTextHandler(newDiagnosticLog(managerFixtureLogLimit), nil)), ctx: context.Background()}
	if err := manager.Release(ctx, "live", "token", "session-end-hook"); err != nil {
		t.Fatal(err)
	}
	if session, err := store.SessionByID(ctx, "live"); err != nil || session.State != "ACTIVE" {
		t.Fatalf("live SessionEnd changed session: state=%s err=%v", session.State, err)
	}
	if err := manager.Release(ctx, "live", "token", "client-exit"); err != nil {
		t.Fatal(err)
	}
	if session, err := store.SessionByID(ctx, "live"); err != nil || session.State != "RELEASING" {
		t.Fatalf("client exit did not release session: state=%s err=%v", session.State, err)
	}
}

func TestAuxiliaryCodexLifecycleDoesNotReplaceOrReleasePrimary(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, err := openTestStoreAtPath(t, filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	session := state.Session{ID: "btw", SlotID: "btw", State: "ACTIVE", AgentKind: "codex", AgentSessionID: "native-primary", TokenHash: state.HashToken("token")}
	if _, err := store.CreateSlotSession(ctx, storeSlotAt(t, store, root, "", "btw", filepath.Join(root, "btw"), 0, "LEASED"), nil, session, ""); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{store: store, jobQueue: newJobQueue(1), log: slog.New(slog.NewTextHandler(newDiagnosticLog(managerFixtureLogLimit), nil)), ctx: context.Background()}
	if err := manager.BindAgentSession(ctx, session.ID, "token", "native-primary"); err != nil {
		t.Fatal(err)
	}

	primary, err := manager.BindAgentSessionFromHook(ctx, session.ID, "token", "native-btw", "fork")
	if err != nil || primary {
		t.Fatalf("auxiliary start: primary=%v err=%v", primary, err)
	}
	result, err := (Handler{Manager: manager}).Handle(ctx, "Release", JSON(map[string]any{
		"session_id": session.ID, "token": "token", "reason": "session-end-hook", "agent_session_id": "native-btw",
	}))
	released := err == nil && result.(map[string]bool)["released"]
	if err != nil || released {
		t.Fatalf("auxiliary end: released=%v err=%v", released, err)
	}
	stored, err := store.SessionByID(ctx, session.ID)
	if err != nil || stored.State != "ACTIVE" || stored.AgentSessionID != "native-primary" {
		t.Fatalf("auxiliary lifecycle changed primary: %+v err=%v", stored, err)
	}
	released, err = manager.ReleaseAgentSession(ctx, session.ID, "token", "session-end-hook", "native-primary")
	if err != nil || !released {
		t.Fatalf("primary end: released=%v err=%v", released, err)
	}
	stored, err = store.SessionByID(ctx, session.ID)
	if err != nil || stored.State != "RELEASING" {
		t.Fatalf("primary end state=%s err=%v", stored.State, err)
	}
}

func TestClaudeClearKeepsLeaseAcrossHookOrder(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		endFirst bool
	}{
		{name: "SessionEnd first", endFirst: true},
		{name: "SessionStart first"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := openTestStoreAtPath(t, filepath.Join(root, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			ctx := context.Background()
			session := state.Session{
				ID: "claude", SlotID: "claude", State: "ACTIVE", AgentKind: "claude",
				ClientPID: os.Getpid(), TokenHash: state.HashToken("token"),
			}
			if _, err := store.CreateSlotSession(ctx, storeSlotAt(t, store, root, "", "claude", filepath.Join(root, "claude"), 0, "LEASED"), nil, session, ""); err != nil {
				t.Fatal(err)
			}
			manager := &Manager{store: store, jobQueue: newJobQueue(1), log: slog.New(slog.NewTextHandler(newDiagnosticLog(managerFixtureLogLimit), nil)), ctx: ctx}
			handler := Handler{Manager: manager}
			start := func(source, nativeID string) {
				t.Helper()
				result, err := handler.Handle(ctx, "BindAgentSession", JSON(map[string]string{
					"session_id": session.ID, "token": "token", "source": source, "agent_session_id": nativeID,
				}))
				if err != nil || result.(map[string]any)["primary"] != true {
					t.Fatalf("SessionStart(%s): result=%v err=%v", source, result, err)
				}
			}
			end := func(nativeID string, matched bool) {
				t.Helper()
				result, err := handler.Handle(ctx, "Release", JSON(map[string]string{
					"session_id": session.ID, "token": "token", "reason": "session-end-hook", "agent_session_id": nativeID,
				}))
				if err != nil || result.(map[string]bool)["released"] != matched {
					t.Fatalf("SessionEnd(%s): result=%v err=%v, want match=%v", nativeID, result, err, matched)
				}
			}
			start("startup", "native-before-clear")
			if test.endFirst {
				end("native-before-clear", true)
			}
			start("clear", "native-after-clear")
			if !test.endFirst {
				end("native-before-clear", false)
			}
			end("native-after-clear", true)
			stored, err := store.SessionByID(ctx, session.ID)
			if err != nil || stored.State != "ACTIVE" || stored.AgentSessionID != "native-after-clear" {
				t.Fatalf("clear changed the active session: session=%+v err=%v", stored, err)
			}
			slot, err := store.Slot(ctx, session.SlotID)
			if err != nil || slot.State != "LEASED" || slot.OwnerSessionID != session.ID {
				t.Fatalf("clear changed the lease: slot=%+v err=%v", slot, err)
			}
		})
	}
}
