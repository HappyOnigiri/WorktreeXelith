package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HappyOnigiri/WorktreeX/internal/gitx"
	"github.com/HappyOnigiri/WorktreeX/internal/testsupport"
)

func TestBranchInspectionDeniesWhenDaemonIsUnreachable(t *testing.T) {
	clearHookEnvironment(t)
	fixture := newBranchPolicyFixture(t)
	t.Setenv("WX_SESSION_ID", "wx")
	t.Setenv("WX_SESSION_TOKEN", "token")
	t.Setenv("WX_DAEMON_SOCKET", testsupport.SocketPath(t, "missing.sock"))
	t.Setenv("WX_WORKSPACE_ROOT", fixture.detached)
	command := strings.Join([]string{"git", "switch", "other", "&&", "git", "rev-parse", "--verify", "HEAD"}, " ")
	payload := recordedPayloadWithCommand(t, "codex-0.151.0-pre-tool-use-exec.json", command)
	payload = strings.Replace(payload, `"cwd":"/tmp/wx-session/root"`, `"cwd":"`+fixture.detached+`"`, 1)
	output := captureHookStdout(t, func() {
		if err := RunHook(context.Background(), "pre-tool-use", strings.NewReader(payload)); err != nil {
			t.Fatalf("hook did not return an attach denial: %v", err)
		}
	})
	var decision preToolUseHookOutput
	if err := json.Unmarshal([]byte(output), &decision); err != nil {
		t.Fatalf("hook output %q: %v", output, err)
	}
	specific := decision.HookSpecificOutput
	if specific.PermissionDecision != "deny" || specific.PermissionDecisionReason != branchAttachReason || specific.UpdatedInput != nil {
		t.Fatalf("decision=%+v, want the original attach denial", specific)
	}
}

func TestPreToolUseDetachesBranchForCommitInspection(t *testing.T) {
	fixture := newBranchPolicyFixture(t)
	fixtureGit(t, fixture.main, "commit", "--allow-empty", "-qm", "inspected")
	fixtureGit(t, fixture.main, "branch", "-f", "other", "HEAD")
	ctx := context.Background()
	runner := &gitx.Runner{Timeout: branchPolicyGitTimeout}
	tip, err := runner.Run(ctx, fixture.main, "rev-parse", "refs/heads/other")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, cwd, command, inspection, fixtureName string
		removeBranch                                bool
	}{
		{"checkout", fixture.detached, "git checkout 'other'", "git rev-parse --verify HEAD", "claude-2.1.258-pre-tool-use.json", false},
		{"switch", fixture.tmp, "cd " + shellSingleQuote(fixture.detached) + " && git switch other", "git show -s --format=%H HEAD", "codex-0.151.0-pre-tool-use-exec.json", false},
		{"git-C", fixture.tmp, "git -C detached checkout other", "git -C detached log -1 --format=%H", "claude-2.1.258-pre-tool-use.json", false},
		{"deleted-branch", fixture.detached, "git " + "switch other", "git rev-parse --verify HEAD", "claude-2.1.258-pre-tool-use.json", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixtureGit(t, fixture.main, "branch", "-f", "other", "HEAD^")
			fixtureGit(t, fixture.detached, "checkout", "--detach", strings.TrimSpace(tip.Stdout)+"^")
			command := test.command + " && " + test.inspection
			payload := recordedPayloadWithCommand(t, test.fixtureName, command)
			payload = strings.Replace(payload, `"cwd":"/tmp/wx-session/root"`, `"cwd":"`+test.cwd+`"`, 1)
			output, ok := preToolUseDecision(ctx, []byte(payload), fixture.detached, true)
			specific := output.HookSpecificOutput
			if !ok || specific.PermissionDecision != "allow" {
				t.Fatalf("decision=%+v, want a rewrite", specific)
			}
			var rewritten string
			if err := json.Unmarshal(specific.UpdatedInput["command"], &rewritten); err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(rewritten, " && "+test.inspection) {
				t.Fatalf("rewritten command changed the inspection: %q", rewritten)
			}
			if test.removeBranch {
				fixtureGit(t, fixture.main, "branch", "-D", "other")
				fixtureGit(t, fixture.main, "tag", "refs/heads/other", "HEAD")
				t.Cleanup(func() { fixtureGit(t, fixture.main, "branch", "other", "HEAD") })
			} else {
				fixtureGit(t, fixture.main, "branch", "-f", "other", "HEAD")
			}
			shell := exec.Command("bash", "-c", rewritten)
			shell.Dir = test.cwd
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "GIT_") {
					shell.Env = append(shell.Env, entry)
				}
			}
			shell.Env = append(shell.Env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
			got, err := shell.Output()
			if test.removeBranch {
				if err == nil || len(got) != 0 {
					t.Fatalf("inspection ran after the branch was removed: output=%q err=%v", got, err)
				}
				return
			}
			if err != nil || strings.TrimSpace(string(got)) != strings.TrimSpace(tip.Stdout) {
				t.Fatalf("inspection output=%q err=%v, want %q", got, err, tip.Stdout)
			}
			_, err = runner.Run(gitx.WithExpectedExitCodes(ctx, 1), fixture.detached, "symbolic-ref", "-q", "HEAD")
			var gitErr *gitx.Error
			if !errors.As(err, &gitErr) || gitErr.Result.ExitCode != 1 {
				t.Fatalf("HEAD did not stay detached: %v", err)
			}
		})
	}
	tipAfter, err := runner.Run(ctx, fixture.main, "rev-parse", "refs/heads/other")
	if err != nil || tipAfter.Stdout != tip.Stdout {
		t.Fatalf("branch ref changed: %q err=%v", tipAfter.Stdout, err)
	}
}

func TestPreToolUseKeepsAttachDenialsOutsideCommitInspection(t *testing.T) {
	fixture := newBranchPolicyFixture(t)
	fixtureGit(t, fixture.main, "branch", "nested/topic")
	if err := os.Symlink(fixture.attached, filepath.Join(fixture.detached, "other-worktree")); err != nil {
		t.Fatal(err)
	}
	switchAndInspect := "git -C %[1]s %s && git -C %[1]s rev-parse HEAD"
	for _, command := range []string{
		fmt.Sprintf(switchAndInspect, shellSingleQuote(fixture.attached), "switch other"),
		fmt.Sprintf(switchAndInspect, "other-worktree", "switch other"),
		"git checkout -b new-branch",
		"git checkout -b new-branch && git rev-parse HEAD",
		"git -C \"$TARGET\" checkout other && git rev-parse HEAD",
		"git checkout other && git rev-parse HEAD && git switch -c new-branch",
		"git switch remote-only && git rev-parse HEAD",
		"git switch nested && git rev-parse HEAD",
		"git checkout other && git rev-parse --abbrev-ref HEAD",
		"git checkout other && git rev-parse '@{upstream}'",
		"git checkout other && git show --decorate HEAD",
		"git checkout other && git push",
		"git checkout other && git -C ../main rev-parse HEAD",
		"git checkout other && git rev-parse HEAD; git status",
		"git checkout other; git rev-parse HEAD",
		"git checkout other || git rev-parse HEAD",
		"git checkout other | git rev-parse HEAD",
		"git checkout other && (git rev-parse HEAD)",
		"git checkout other && git rev-parse HEAD$(git switch -c new-branch)",
	} {
		t.Run(command, func(t *testing.T) {
			payload := recordedPayloadWithCommand(t, "claude-2.1.258-pre-tool-use.json", command)
			payload = strings.Replace(payload, `"cwd":"/tmp/wx-session/root"`, `"cwd":"`+fixture.detached+`"`, 1)
			output, ok := preToolUseDecision(context.Background(), []byte(payload), fixture.detached, true)
			if !ok || output.HookSpecificOutput.PermissionDecision != "deny" || output.HookSpecificOutput.UpdatedInput != nil {
				t.Fatalf("decision=%+v, want a denial", output.HookSpecificOutput)
			}
		})
	}
}
