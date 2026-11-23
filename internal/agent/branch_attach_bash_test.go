package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyBranchAttachInLiteralBashCommand(t *testing.T) {
	// 分類関数はテストプロセスの環境を読むので、実行元の設定をケースから切り離す。
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		if policyBashEnvironmentEntryKnown(name, value) {
			continue
		}
		t.Setenv(name, value)
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	fixture := newBranchPolicyFixture(t)
	inline := func(script string) string { return "bash -c '" + script + "'" }
	switchOther := "git " + "switch other"
	detach := "git " + "switch --detach other"
	runBranchPolicyCases(t, []branchPolicyCase{
		{command: inline(detach), cwd: fixture.detached, want: branchPolicyAllow},
		{command: inline(switchOther), cwd: fixture.detached, want: branchPolicyAttach},
		{command: inline("git " + "checkout other"), cwd: fixture.detached, want: branchPolicyAttach},
		{command: inline("git " + "symbolic-ref HEAD refs/heads/other"), cwd: fixture.detached, want: branchPolicyAttach},
		{command: inline("cd ../main && " + switchOther), cwd: fixture.detached, want: branchPolicyAllow},
		{command: `/bin/bash -c "cd ../detached && ` + switchOther + `"`, cwd: fixture.main, want: branchPolicyAttach},
		{command: inline("cd ../main &&\n " + switchOther + "\n"), cwd: fixture.detached, want: branchPolicyAllow},
		{command: inline("git -C ../main " + "switch other"), cwd: fixture.detached, want: branchPolicyAllow},
		{command: inline("git -C ../detached " + "switch other"), cwd: fixture.main, want: branchPolicyAttach},
		{command: inline("cd ../main && "+switchOther) + " && " + switchOther, cwd: fixture.detached, want: branchPolicyAttach},
		{command: inline("cd ../detached && "+detach) + "; " + switchOther, cwd: fixture.main, want: branchPolicyAllow},
		{command: inline(detach) + " || echo detach failed", cwd: fixture.detached, want: branchPolicyAllow},
		{command: "cd ../main || " + inline(detach), cwd: fixture.detached, want: branchPolicyUnresolved},
		{command: "cd ../detached && " + inline(switchOther), cwd: fixture.main, want: branchPolicyAttach},
		{command: inline(`cd "$REPO" && ` + switchOther), cwd: fixture.detached, want: branchPolicyUnresolved},
		{command: inline(`git -C "$REPO" ` + "switch other"), cwd: fixture.main, want: branchPolicyUnresolved},
		{command: inline("git " + `switch "$REF"`), cwd: fixture.detached, want: branchPolicyUnresolved},
		{command: inline("cd ../main; " + switchOther), cwd: fixture.detached, want: branchPolicyUnresolved},
		{command: inline(detach) + " name", cwd: fixture.detached, want: branchPolicyUnresolved},
		{command: "BASH_ENV=./startup " + inline(detach), cwd: fixture.detached, want: branchPolicyUnresolved},
		{command: "export BASH_ENV=./startup; " + inline(detach), cwd: fixture.detached, want: branchPolicyUnresolved},
		{command: inline(detach) + ` > "$LOG"`, cwd: fixture.detached, want: branchPolicyUnresolved},
	})
	t.Run("inherited Git config", func(t *testing.T) {
		t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
		runBranchPolicyCases(t, []branchPolicyCase{
			{command: inline(detach), cwd: fixture.detached, want: branchPolicyUnresolved},
		})
	})
	t.Run("logical PWD", func(t *testing.T) {
		link := filepath.Join(fixture.tmp, "linked")
		if err := os.Symlink(fixture.detached, link); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PWD", link)
		runBranchPolicyCases(t, []branchPolicyCase{
			{command: inline(detach), cwd: fixture.detached, want: branchPolicyAllow},
			{command: inline("cd ../detached && " + detach), cwd: fixture.detached, want: branchPolicyUnresolved},
			{command: "cd ../detached && " + inline(detach), cwd: fixture.detached, want: branchPolicyUnresolved},
		})
	})
	t.Setenv("BASH_ENV", filepath.Join(fixture.tmp, "startup"))
	runBranchPolicyCases(t, []branchPolicyCase{
		{command: inline(detach), cwd: fixture.detached, want: branchPolicyUnresolved},
	})
}
