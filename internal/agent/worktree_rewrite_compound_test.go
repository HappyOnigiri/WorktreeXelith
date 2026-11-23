package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// compoundFixture は server と web の 2 repository を持つ workspace root と、その外の repository を作る。
func compoundFixture(t *testing.T) (root, outside string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{filepath.Join(root, "server", ".git"), filepath.Join(root, "web", ".git"), filepath.Join(outside, "repo", ".git")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return root, outside
}

// TestClassifyWorktreeAddCommandRewritesCompound は、変数または直後の cd に貸出先を渡し、
// 残りの文字列をそのまま残すことを確認する。
func TestClassifyWorktreeAddCommandRewritesCompound(t *testing.T) {
	root, _ := compoundFixture(t)
	server := filepath.Join(root, "server")
	oid := "3139d393c8470d47e7cf30810580acad50a907e7"
	nested := filepath.Join(root, "web", "server")
	if err := os.MkdirAll(filepath.Join(nested, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("server", filepath.Join(root, "server-link")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, cwd, command, rewrite string
	}{
		{
			name:    "literal path",
			cwd:     server,
			command: `git worktree add x && cd x`,
			rewrite: `wx_worktree_path=$(` + compoundCDPATHGuard + `wx new) && echo "wx_worktree_path=$wx_worktree_path" && cd -- "$wx_worktree_path"`,
		},
		{
			name:    "literal path with a continued line",
			cwd:     server,
			command: "git worktree add x &&\n  cd ./x &&\n  cat marker",
			rewrite: "wx_worktree_path=$(wx new) && echo \"wx_worktree_path=$wx_worktree_path\" && cd -- \"$wx_worktree_path\" &&\n  cat marker",
		},
		{
			name:    "quoted path and repository option",
			cwd:     root,
			command: `git -C server worktree add --detach 'new tree' origin/main && cd -- "./server/new tree" && cat file 2>/dev/null | head`,
			rewrite: `wx_worktree_path=$(cd '` + server + `' && wx new --branch main) && echo "wx_worktree_path=$wx_worktree_path" && cd -- "$wx_worktree_path" && cat file 2>/dev/null | head`,
		},
		{
			name:    "repository symlink and missing target parents",
			cwd:     root,
			command: `git -C server-link worktree add new/x && cd ./server-link/new/x`,
			rewrite: `wx_worktree_path=$(cd '` + server + `' && wx new) && echo "wx_worktree_path=$wx_worktree_path" && cd -- "$wx_worktree_path"`,
		},
		{
			name:    "command cd before a relative repository option",
			cwd:     root,
			command: `command -p -- cd web && git -C server worktree add /tmp/x && cd /tmp/x`,
			rewrite: `command -p -- cd web && wx_worktree_path=$(cd '` + nested + `' && wx new) && echo "wx_worktree_path=$wx_worktree_path" && cd -- "$wx_worktree_path"`,
		},
		{
			name:    "cd into a repository before the candidate",
			cwd:     root,
			command: "cd " + server + "; WT=" + root + "/worktrees/server-review-fix-pr-11534; git worktree add --detach \"$WT\" " + oid + " && cd $WT && cat test",
			rewrite: "cd " + server + "; WT=" + root + "/worktrees/server-review-fix-pr-11534; WT=$(wx new --commit " + oid + `) && echo "WT=$WT" && cd $WT && cat test`,
		},
		{
			name:    "directory option after a pipeline",
			cwd:     root,
			command: "git -C server fetch origin -q 2>&1 | tail -2; ls worktrees 2>/dev/null; WT=" + root + "/worktrees/server-check; git -C server worktree add --detach \"$WT\" 31e62bf37726",
			rewrite: "git -C server fetch origin -q 2>&1 | tail -2; ls worktrees 2>/dev/null; WT=" + root + "/worktrees/server-check; WT=$(cd '" + server + `' && wx new --commit 31e62bf37726) && echo "WT=$WT"`,
		},
		{
			name:    "command substitution before the candidate",
			cwd:     server,
			command: `WT=$(mktemp -d) && git worktree add --detach "$WT" origin/main && cd "$WT" && make test`,
			rewrite: `WT=$(mktemp -d) && WT=$(wx new --branch main) && echo "WT=$WT" && cd "$WT" && make test`,
		},
		{
			name:    "braced variable",
			cwd:     server,
			command: `git worktree add -q --detach ${W} HEAD && ls ${W}`,
			rewrite: `W=$(wx new) && echo "W=$W" && ls ${W}`,
		},
		{
			name:    "workspace root then directory option",
			cwd:     server,
			command: `cd .. && git -C server worktree add "$WT" ` + oid + ` && cd "$WT"`,
			rewrite: `cd .. && WT=$(cd '` + server + `' && wx new --commit ` + oid + `) && echo "WT=$WT" && cd "$WT"`,
		},
		{
			name:    "dot as an argument",
			cwd:     server,
			command: `ls . && git worktree add "$WT" HEAD && cd "$WT"`,
			rewrite: `ls . && WT=$(wx new) && echo "WT=$WT" && cd "$WT"`,
		},
		{
			name:    "heredoc after the candidate",
			cwd:     server,
			command: "git worktree add \"$WT\" HEAD && cat <<'EOF'\nnote\nEOF",
			rewrite: "WT=$(wx new) && echo \"WT=$WT\" && cat <<'EOF'\nnote\nEOF",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verdict := classifyWorktreeAddCommand(t.Context(), test.command, worktreeAddLocation{cwd: test.cwd, root: root})
			if verdict.decision != worktreeAddRewrite || !verdict.compound || verdict.command != test.rewrite {
				t.Fatalf("verdict=(decision=%d compound=%v command=%q reason=%q), want %q", verdict.decision, verdict.compound, verdict.command, verdict.reason, test.rewrite)
			}
		})
	}
}

// TestClassifyWorktreeAddCommandKeepsUnsafeCompoundsDenied は、失敗時の後続・実行先・残る worktree 作成を
// 静的に決められない連結を、書き換えずに拒否することを確認する。
func TestClassifyWorktreeAddCommandKeepsUnsafeCompoundsDenied(t *testing.T) {
	root, outside := compoundFixture(t)
	server := filepath.Join(root, "server")
	target := filepath.Join(root, "sandbox")
	alias := filepath.Join(root, "shortcut")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	presentTarget := filepath.Join(root, "present-tree")
	if err := os.Mkdir(presentTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	presentAlias := filepath.Join(root, "present-link")
	if err := os.Symlink(presentTarget, presentAlias); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, cwd, command string
	}{
		{name: "cd out of the workspace", cwd: root, command: "cd " + filepath.Join(outside, "repo") + ` && WT=/tmp/x && git worktree add --detach "$WT" 3139d39`},
		{name: "cd to an expanded directory", cwd: root, command: `cd "$REPO" && git worktree add "$WT" HEAD && cd "$WT"`},
		{name: "relative cd without cwd", cwd: "", command: `cd server && git worktree add "$WT" HEAD`},
		{name: "cd to a directory outside any repository", cwd: server, command: `cd .. && git worktree add "$WT" HEAD`},
		{name: "conditional cd followed by semicolon", cwd: root, command: `test -d server && cd server; git worktree add "$WT" HEAD && cd "$WT"`},
		{name: "cd after or", cwd: root, command: `false || cd server && git worktree add "$WT" HEAD`},
		{name: "pipe after the candidate", cwd: server, command: `git worktree add --detach "$WT" 3139d39 2>&1 | tail -1 && cd "$WT"`},
		{name: "semicolon after the candidate", cwd: server, command: `git worktree add --detach "$WT" 3139d39; cd "$WT"`},
		{name: "or before the candidate", cwd: server, command: `false || git worktree add "$WT" HEAD && cd "$WT"`},
		{name: "literal path without cd", cwd: server, command: `git worktree add x && ls`},
		{name: "different literal path", cwd: server, command: `git worktree add x && cd y`},
		{name: "literal path from a workspace root outside any repository", cwd: root, command: `git worktree add x && cd ./x`},
		{name: "different bases with git -C", cwd: root, command: `git -C server worktree add x && cd x`},
		{name: "literal path with semicolon", cwd: server, command: `git worktree add x; cd x`},
		{name: "duplicate operator before cd", cwd: server, command: `git worktree add x && && cd x`},
		{name: "semicolon after cd", cwd: server, command: `git worktree add x && cd x; pwd`},
		{name: "or after suffix", cwd: server, command: `git worktree add x && cd x && true || pwd`},
		{name: "newline after suffix", cwd: server, command: "git worktree add x && cd x && true\npwd"},
		{name: "literal path reused in argument", cwd: server, command: `git worktree add /tmp/x && cd /tmp/x && tool --path=/tmp/x/file`},
		{name: "literal path reused with different case", cwd: server, command: `git worktree add /tmp/old-tree && cd /tmp/old-tree && cat /tmp/OLD-TREE/file`},
		{name: "literal path reached through dangling symlink", cwd: server, command: "git worktree add " + target + " && cd " + target + " && cat " + alias + "/marker"},
		{name: "literal path reached through redirect symlink", cwd: server, command: "git worktree add " + presentTarget + " && cd " + presentTarget + " && echo note >" + presentAlias + "/marker"},
		{name: "literal path reused in redirect", cwd: server, command: `git worktree add /tmp/x && cd /tmp/x && echo yes >/tmp/x/file`},
		{name: "literal path hidden in heredoc", cwd: server, command: "git worktree add x && cd x && cat <<'EOF'\nx\nEOF"},
		{name: "literal path through expansion", cwd: server, command: `git worktree add x && cd x && cat "$OLDPWD/x"`},
		{name: "glob in path", cwd: server, command: `git worktree add x* && cd x*`},
		{name: "assignment name already in use", cwd: server, command: `wx_worktree_path=other; git worktree add x && cd x`},
		{name: "path built from a variable", cwd: server, command: `git worktree add --detach $S/wt 3139d39 && cd $S/wt`},
		{name: "reserved variable", cwd: server, command: `git worktree add "$HOME" HEAD && ls`},
		{name: "subshell around the candidate", cwd: server, command: `(git worktree add "$WT" HEAD) && cd "$WT"`},
		{name: "subshell with cd", cwd: root, command: `true && (cd server && git worktree add "$WT" HEAD)`},
		{name: "control flow before the candidate", cwd: server, command: `for d in a; do :; done; git worktree add "$WT" HEAD && cd "$WT"`},
		{name: "two candidates", cwd: server, command: `git worktree add "$A" HEAD && git worktree add "$B" HEAD`},
		{name: "candidate in a substitution", cwd: server, command: `git worktree add "$WT" HEAD && echo "$(git worktree add y)"`},
		{name: "candidate behind a heredoc", cwd: server, command: "git worktree add \"$WT\" HEAD && cat <<'EOF'\nnote\nEOF\ngit worktree add y"},
		{name: "eval after the candidate", cwd: server, command: `git worktree add "$WT" HEAD && eval "$NEXT"`},
		{name: "nested shell after the candidate", cwd: server, command: `git worktree add "$WT" HEAD && sh -c 'git worktree add y'`},
		{name: "nested shell behind env", cwd: server, command: `git worktree add "$WT" HEAD && env FOO=1 sh -c 'git worktree add y'`},
		{name: "nested shell behind sudo", cwd: server, command: `git worktree add "$WT" HEAD && sudo -u me bash -c 'make'`},
		{name: "candidate behind nohup", cwd: server, command: `git worktree add "$WT" HEAD && nohup git worktree add y HEAD`},
		{name: "candidate behind env", cwd: server, command: `git worktree add "$WT" HEAD && env GIT_DIR=/other git worktree add y`},
		{name: "source with a dot", cwd: server, command: `. ./env.sh && git worktree add "$WT" HEAD`},
		{name: "branch creation", cwd: server, command: `git worktree add -b topic "$WT" HEAD && cd "$WT"`},
		{name: "directory option outside the workspace", cwd: server, command: "git -C " + filepath.Join(outside, "repo") + ` worktree add "$WT" HEAD && cd "$WT"`},
		{name: "redirect on the candidate", cwd: server, command: `git worktree add "$WT" HEAD >/dev/null && cd "$WT"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verdict := classifyWorktreeAddCommand(t.Context(), test.command, worktreeAddLocation{cwd: test.cwd, root: root})
			if verdict.decision != worktreeAddDeny || verdict.reason != denyReasonCompound {
				t.Fatalf("verdict=(decision=%d command=%q reason=%q), want a compound deny", verdict.decision, verdict.command, verdict.reason)
			}
		})
	}
}

// TestPreToolUseDecisionRunsLiteralWorktreeChain は、貸出成功時だけ後続が貸出先で走ることを Bash で確認する。
func TestPreToolUseDecisionRunsLiteralWorktreeChain(t *testing.T) {
	root, _ := compoundFixture(t)
	server := filepath.Join(root, "server")
	leased := filepath.Join(root, "lease with space")
	if err := os.Mkdir(leased, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leased, "marker"), []byte("inside lease\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"cwd": root, "tool_input": map[string]string{
			"command": `git -C server worktree add --detach x origin/main && cd server/x && pwd && cat marker`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	output, ok := preToolUseDecision(t.Context(), payload, root, true)
	if !ok || output.HookSpecificOutput.PermissionDecision != "allow" {
		t.Fatalf("hook decision = %+v, want allow", output.HookSpecificOutput)
	}
	var rewritten string
	if err := json.Unmarshal(output.HookSpecificOutput.UpdatedInput["command"], &rewritten); err != nil {
		t.Fatal(err)
	}
	stub := `wx() {
		printf '%s %s\n' "$PWD" "$*" >&2
		printf '%s\n' "$WX_TEST_LEASE"
		return "$WX_TEST_EXIT"
	}
	`
	wantCall := server + " new --branch main\n"
	for _, tc := range []struct {
		name, leaseStatus, cdpath, output string
		exitCode                          int
	}{
		{"success", "0", "", wantCall + "wx_worktree_path=" + leased + "\n" + leased + "\ninside lease\n", 0},
		{"empty CDPATH components", "0", "::", wantCall + "wx_worktree_path=" + leased + "\n" + leased + "\ninside lease\n", 0},
		{"failure", "27", "", wantCall, 27},
		{"CDPATH search", "0", root, "wx: CDPATH may change the cd destination; retry with an absolute or ./-prefixed cd path.\n", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "/bin/bash", "-c", stub+rewritten)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "BASH_ENV=", "WX_TEST_LEASE="+leased, "WX_TEST_EXIT="+tc.leaseStatus, "CDPATH="+tc.cdpath)
			got, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if tc.exitCode == 0 {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.As(err, &exit) || exit.ExitCode() != tc.exitCode {
				t.Fatalf("Bash error = %v, want exit %d", err, tc.exitCode)
			}
			if string(got) != tc.output {
				t.Fatalf("Bash output = %q, want %q", got, tc.output)
			}
		})
	}
}

// TestLiteralWorktreeCompoundPreservesExplicitHead は貸出元の HEAD が既定ブランチと異なる場合を確認する。
func TestLiteralWorktreeCompoundPreservesExplicitHead(t *testing.T) {
	fixture := newBranchPolicyFixture(t)
	fixtureGit(t, fixture.detached, "commit", "-q", "--allow-empty", "-m", "detached commit")
	detached := literalWorktreeHead(t.Context(), fixture.detached)
	if detached == "" || detached == literalWorktreeHead(t.Context(), fixture.main) {
		t.Fatal("fixture must have a detached HEAD different from main")
	}

	command := `git worktree add --detach next HEAD && cd ./next`
	location := worktreeAddLocation{cwd: fixture.detached, root: fixture.detached}
	verdict := classifyWorktreeAddCommand(t.Context(), command, location)
	if verdict.decision != worktreeAddRewrite || !strings.Contains(verdict.command, "wx new --commit "+detached+")") {
		t.Fatalf("rewrite = %+v, want detached HEAD %s", verdict, detached)
	}
	verdict = classifyWorktreeAddCommand(t.Context(), "true && "+command, location)
	if verdict.decision != worktreeAddDeny {
		t.Fatalf("rewrite after a preceding command = %+v, want deny", verdict)
	}
}

// TestPreToolUseDecisionChecksBranchAttachAfterCompoundRewrite は、連結を書き換えて allow を返す前に
// 書き換え後の command をブランチ attach の判定へ通すことを確認する。
func TestPreToolUseDecisionChecksBranchAttachAfterCompoundRewrite(t *testing.T) {
	root, _ := compoundFixture(t)
	server := filepath.Join(root, "server")
	decide := func(t *testing.T, command string, managed bool) preToolUseHookOutput {
		t.Helper()
		payload, err := json.Marshal(map[string]any{"cwd": server, "tool_input": map[string]any{"command": command, "description": "lease"}})
		if err != nil {
			t.Fatal(err)
		}
		output, ok := preToolUseDecision(context.Background(), payload, root, managed)
		if !ok {
			t.Fatalf("command %q produced no decision", command)
		}
		return output
	}
	for _, path := range []string{`"$WT"`, "x"} {
		attach := `git worktree add --detach ` + path + ` origin/main && cd ` + path + ` && git checkout -b topic`
		if output := decide(t, attach, true); output.HookSpecificOutput.PermissionDecision != "deny" {
			t.Fatalf("managed attach after rewrite=%+v, want deny", output.HookSpecificOutput)
		}
		if output := decide(t, attach, false); output.HookSpecificOutput.PermissionDecision != "allow" {
			t.Fatalf("direct attach after rewrite=%+v, want allow", output.HookSpecificOutput)
		}
	}
	restore := "checkout -- tracked.txt"
	for _, tc := range []struct{ path, decision string }{{"x", "allow"}, {`"$WT"`, "deny"}} {
		command := "git worktree add --detach " + tc.path + " origin/main && cd " + tc.path + " && git " + restore
		if got := decide(t, command, true).HookSpecificOutput.PermissionDecision; got != tc.decision {
			t.Errorf("path restore after rewrite = %s, want %s for %s", got, tc.decision, tc.path)
		}
	}
	output := decide(t, `git worktree add --detach "$WT" HEAD && cd "$WT" && make test`, true)
	specific := output.HookSpecificOutput
	var command string
	if err := json.Unmarshal(specific.UpdatedInput["command"], &command); err != nil {
		t.Fatal(err)
	}
	if specific.PermissionDecision != "allow" || command != `WT=$(wx new) && echo "WT=$WT" && cd "$WT" && make test` {
		t.Fatalf("rewrite=%+v command=%q", specific, command)
	}
	if specific.AdditionalContext != compoundRewriteContext || string(specific.UpdatedInput["description"]) != `"lease"` {
		t.Fatalf("rewrite context=%q updatedInput=%v", specific.AdditionalContext, specific.UpdatedInput)
	}
	if !strings.Contains(specific.AdditionalContext, "NAME=<path>") {
		t.Fatalf("compound context=%q does not explain the printed path", specific.AdditionalContext)
	}
}

// TestRewrittenPayloadKeepsThePayloadWhenItCannotBeDecoded は、組み立てられない payload を元のまま返すことを確認する。
func TestRewrittenPayloadKeepsThePayloadWhenItCannotBeDecoded(t *testing.T) {
	if got := string(rewrittenPayload([]byte("{"), map[string]json.RawMessage{"command": json.RawMessage(`"x"`)})); got != "{" {
		t.Fatalf("rewrittenPayload=%q, want the original payload", got)
	}
}
