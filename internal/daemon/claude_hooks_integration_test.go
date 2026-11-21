package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HappyOnigiri/WorktreeX/internal/agent"
	"github.com/HappyOnigiri/WorktreeX/internal/rpc"
	"github.com/HappyOnigiri/WorktreeX/internal/testsupport"
)

func TestClaudeResumeHooksKeepLeaseAndSnapshotLastConversation(t *testing.T) {
	requireDaemonIntegration(t)
	f := runningManagerFixture(t, func(s *managerFixtureSetup) {
		s.Config.Pool.WarmPerWorkspace = 0
		s.Config.Discovery.ReconcileInterval.Duration = time.Hour
	})
	repo := filepath.Join(f.Root, "repo")
	initGitRepo(t, repo)
	ctx := context.Background()
	lease, err := f.Manager.ResolveAndLease(ctx, repo, nil, "claude", 0, leaseAttrs{})
	if err != nil {
		t.Fatal(err)
	}
	if err := waitReady(ctx, f.Manager, 15*time.Second, lease.SessionID, lease.Token); err != nil {
		t.Fatal(err)
	}
	initial, err := f.Store.SessionByID(ctx, lease.SessionID)
	if err != nil {
		t.Fatal(err)
	}

	writer := exec.Command("sleep", "30")
	if err := writer.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if writer.ProcessState == nil {
			_ = writer.Process.Kill()
			_ = writer.Wait()
		}
	})
	if err := f.Manager.RegisterAgentProcess(ctx, lease.SessionID, lease.Token, writer.Process.Pid); err != nil {
		t.Fatal(err)
	}

	socket := testsupport.SocketPath(t, "wxd.sock")
	serverCtx, cancel := context.WithCancel(ctx)
	server := &rpc.Server{Socket: socket, Handler: Handler{Manager: f.Manager}, Durable: f.Store}
	done := make(chan error, 1)
	go func() { done <- server.Serve(serverCtx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	testsupport.WaitForSocket(t, socket, done)
	t.Setenv("WX_DAEMON_SOCKET", socket)
	t.Setenv("WX_SESSION_ID", lease.SessionID)
	t.Setenv("WX_SESSION_TOKEN", lease.Token)
	t.Setenv("WX_RECOVERY_DISCARDED", "")

	runHook := func(event, source, nativeID string) {
		t.Helper()
		payload := fmt.Sprintf(`{"source":%q,"session_id":%q}`, source, nativeID)
		if err := agent.RunHook(ctx, event, strings.NewReader(payload)); err != nil {
			t.Fatalf("%s %s %s: %v", event, source, nativeID, err)
		}
	}
	assertLeased := func(nativeID string) {
		t.Helper()
		session, err := f.Store.SessionByID(ctx, lease.SessionID)
		if err != nil || session.State != "ACTIVE" || session.AgentSessionID != nativeID || session.SlotID != initial.SlotID {
			t.Fatalf("want active conversation %s in the same slot: session=%+v err=%v", nativeID, session, err)
		}
		slot, err := f.Store.Slot(ctx, initial.SlotID)
		if err != nil || slot.State != "LEASED" || slot.OwnerSessionID != lease.SessionID {
			t.Fatalf("lease changed: slot=%+v err=%v", slot, err)
		}
	}
	runHook("session-start", "startup", "native-a")
	assertLeased("native-a")
	runHook("session-end", "", "native-a")
	assertLeased("native-a")
	for _, event := range []struct{ source, nativeID string }{
		{source: "clear", nativeID: "native-b"},
		{source: "resume", nativeID: "native-a"},
		{source: "resume", nativeID: "native-b"},
		{source: "resume", nativeID: "native-a"},
	} {
		runHook("session-start", event.source, event.nativeID)
		assertLeased(event.nativeID)
	}

	if err := os.WriteFile(filepath.Join(lease.Path, "tracked.txt"), []byte("conversation work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writer.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = writer.Wait()
	runHook("session-end", "", "native-b")
	assertLeased("native-a")
	runHook("session-end", "", "native-a")
	waitUntil(t, 10*time.Second, func() bool {
		session, err := f.Store.SessionByID(ctx, lease.SessionID)
		return err == nil && session.State == "ARCHIVED"
	})
	saved, err := f.Store.FindByAgentSession(ctx, "claude", "native-a")
	if err != nil || saved.ID != lease.SessionID || saved.State != "ARCHIVED" {
		t.Fatalf("last conversation does not resolve to the archived lease: session=%+v err=%v", saved, err)
	}
	snapshots, err := f.Store.Snapshots(ctx, saved.ID)
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshot count=%d err=%v", len(snapshots), err)
	}
	if got := gitOutput(t, repo, "show", snapshots[0].WorktreeRef+":tracked.txt"); got != "conversation work" {
		t.Fatalf("saved worktree content=%q", got)
	}
}
