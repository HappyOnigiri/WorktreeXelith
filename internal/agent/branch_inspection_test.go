package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/HappyOnigiri/WorktreeX/internal/gitx"
)

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
	}{
		{"checkout", fixture.detached, "git checkout 'other'", "git rev-parse --verify HEAD", "claude-2.1.258-pre-tool-use.json"},
		{"switch", fixture.tmp, "cd " + shellSingleQuote(fixture.detached) + " && git switch other", "git show -s --format=%H HEAD", "codex-0.151.0-pre-tool-use-exec.json"},
		{"git-C", fixture.tmp, "git -C detached checkout other", "git -C detached log -1 --format=%H", "claude-2.1.258-pre-tool-use.json"},
	} {
		t.Run(test.name, func(t *testing.T) {
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
			shell := exec.Command("bash", "-c", rewritten)
			shell.Dir = test.cwd
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "GIT_") {
					shell.Env = append(shell.Env, entry)
				}
			}
			shell.Env = append(shell.Env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
			got, err := shell.Output()
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
	for _, command := range []string{
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
