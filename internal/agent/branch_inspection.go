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

// detachBranchInspection は貸出範囲内で attach と同じ実行先のコミットを読む && 連結だけを書き換える。
// ローカル ref の先端は実行時に取得し、切り替えに失敗したら元の && で後続を止める。
func detachBranchInspection(ctx context.Context, runner *gitx.Runner, command, cwd, workspaceRoot string) (string, bool) {
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
	root, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil || !withinDirectory(root, attach.target) || !insideRepository(attach.target, root) {
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
	result, err := runner.Run(ctx, attach.target, "for-each-ref", "--format=%(refname) %(objecttype)", "--", ref)
	if err != nil {
		return "", false
	}
	if !slices.Contains(strings.Split(result.Stdout, "\n"), ref+" commit") {
		return "", false
	}
	// show-ref --verify は切り替え前にブランチが消えても、同名の tag などに解決しない。
	// 空の出力に ^{commit} を付けた引数は commit として解決できず、切り替えが失敗する。
	lookup := "$(git -C " + shellSingleQuote(attach.target) + " show-ref --verify --hash -- " + shellSingleQuote(ref) + ")"
	arg := segments[0][len(segments[0])-1]
	return command[:arg.start] + `--detach "` + lookup + `^{commit}"` + command[arg.end:], true
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
		case token.operator == "\n" && len(segments) > 0 && start == index:
			// && の後に次のコマンドが来るまでの改行は、コマンドを区切らない。
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
		noDecorate := false
		format := ""
		for _, arg := range args {
			switch arg.value {
			case "HEAD":
				if head {
					return false
				}
				head = true
			case "--no-decorate":
				noDecorate = true
			case "--format=%H", "--format=%h", "--oneline":
				// --oneline も出力形式を指定するため、最後の指定を覚える。
				format = arg.value
			case "-1", "--no-patch", "-s", "--stat":
			default:
				return false
			}
		}
		// format.pretty に含まれる %d は --no-decorate でも ref を表示する。
		return format != "" && (format != "--oneline" || noDecorate)
	default:
		return false
	}
}
