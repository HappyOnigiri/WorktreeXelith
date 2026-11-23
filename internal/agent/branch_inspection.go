package agent

import (
	"context"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/HappyOnigiri/WorktreeX/internal/gitx"
)

var fullGitObjectID = regexp.MustCompile(`^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)

// detachBranchInspection は attach と同じ実行先でコミットを読む && 連結だけを書き換える。
// --detach と解決済みの OID を渡し、後続の原文と checkout 失敗時に止まる && を残す。
func detachBranchInspection(ctx context.Context, runner *gitx.Runner, command, cwd string) (string, bool) {
	segments := branchInspectionSegments(command)
	if len(segments) < 2 {
		return "", false
	}
	base := resolvePolicyDirectory(commandWord{value: cwd}, string(filepath.Separator))
	if first := segments[0]; len(first) == 2 && first[0].word.value == "cd" {
		path := first[1].word
		if !filepath.IsAbs(path.value) || !inspectionDirectory(path) {
			return "", false
		}
		base = resolvePolicyDirectory(path, base)
		segments = segments[1:]
	}
	if base == "" || len(segments) < 2 {
		return "", false
	}
	attach := branchInspectionGit(segments[0], base)
	if attach.target == "" || len(attach.args) != 1 || (attach.subcommand != "checkout" && attach.subcommand != "switch") {
		return "", false
	}
	branch := attach.args[0].value
	if branch == "" || branch == "HEAD" || branch == "@" || strings.HasPrefix(branch, "-") ||
		strings.HasPrefix(branch, "refs/") || fullGitObjectID.MatchString(branch) {
		return "", false
	}
	for _, segment := range segments[1:] {
		inspect := branchInspectionGit(segment, base)
		if inspect.target != attach.target || !inspectsHeadCommit(inspect.subcommand, inspect.args) {
			return "", false
		}
	}
	ref := "refs/heads/" + branch
	// for-each-ref は子の ref にも一致し得るので、完全一致したローカルブランチの commit だけを採る。
	result, err := runner.Run(ctx, attach.target, "for-each-ref", "--format=%(refname) %(objecttype) %(objectname)", "--", ref)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if oid, ok := strings.CutPrefix(line, ref+" commit "); ok && fullGitObjectID.MatchString(oid) {
			arg := segments[0][len(segments[0])-1]
			return command[:arg.start] + "--detach " + shellSingleQuote(oid+"^{commit}") + command[arg.end:], true
		}
	}
	return "", false
}

// branchInspectionSegments は静的な単純コマンドだけを && で区切る。空の要素やリダイレクトなどは受け付けない。
func branchInspectionSegments(command string) [][]policyToken {
	if strings.ContainsAny(command, "\x00\r") {
		return nil
	}
	tokens, ok := lexPolicyCommand(command)
	if !ok {
		return nil
	}
	for len(tokens) > 0 && tokens[len(tokens)-1].operator == "\n" {
		tokens = tokens[:len(tokens)-1]
	}
	var segments [][]policyToken
	start := 0
	for index, token := range tokens {
		switch {
		case token.kind == policyTokenWord && token.word.static() && !token.word.tilde:
		case token.operator == "&&" && start < index:
			segments = append(segments, tokens[start:index])
			start = index + 1
		default:
			return nil
		}
	}
	if start == len(tokens) {
		return nil
	}
	return append(segments, tokens[start:])
}

func branchInspectionGit(segment []policyToken, base string) policyGitInvocation {
	if segment[0].word.value != "git" {
		return policyGitInvocation{}
	}
	args := make([]commandWord, 0, len(segment)-1)
	for _, token := range segment[1:] {
		args = append(args, token.word)
	}
	if len(args) >= 3 && args[0].value == "-C" && inspectionDirectory(args[1]) {
		base = resolvePolicyDirectory(args[1], base)
		args = args[2:]
	}
	if len(args) == 0 {
		return policyGitInvocation{}
	}
	return policyGitInvocation{subcommand: args[0].value, args: args[1:], target: base}
}

// inspectionDirectory は symlink の後の .. が字句上の正規化で別の実行先になるのを防ぐ。
func inspectionDirectory(path commandWord) bool {
	return path.value != "" && !slices.Contains(strings.Split(path.value, "/"), "..")
}

// inspectsHeadCommit は HEAD のブランチ名や upstream に依存しない読み取りだけを受け付ける。
func inspectsHeadCommit(subcommand string, args []commandWord) bool {
	switch subcommand {
	case "rev-parse":
		if len(args) == 2 && args[0].value == "--verify" {
			args = args[1:]
		}
		return len(args) == 1 && (args[0].value == "HEAD" || args[0].value == "HEAD^{commit}")
	case "show", "log":
		head := false
		for _, arg := range args {
			switch arg.value {
			case "HEAD":
				if head {
					return false
				}
				head = true
			case "-1", "--oneline", "--no-patch", "-s", "--stat", "--format=%H", "--format=%h":
			default:
				return false
			}
		}
		return true
	default:
		return false
	}
}
