package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/HappyOnigiri/WorktreeX/internal/gitx"
)

// policyBashInvocation は引数が固定の bash -c から、引き継いだ cwd で走る Git 呼び出しを読む。
// 本文はリテラルな cd を && で連ね、最後に Git を呼ぶ形に限る。
func policyBashInvocation(parts []commandWord, base string, logicalCWDKnown bool) (policyGitInvocation, bool) {
	if len(parts) != 3 || (parts[0].value != "bash" && parts[0].value != "/bin/bash") ||
		parts[1].value != "-c" || !literalPolicyWord(parts[0]) || !literalPolicyWord(parts[1]) || !literalPolicyWord(parts[2]) {
		return policyGitInvocation{}, false
	}
	if strings.ContainsRune(parts[2].value, '\r') || !policyBashEnvironmentKnown() {
		return policyGitInvocation{}, false
	}
	tokens, ok := lexPolicyCommand(parts[2].value)
	if !ok {
		return policyGitInvocation{}, false
	}
	for len(tokens) > 0 && tokens[len(tokens)-1].operator == "\n" {
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens) > 0 && tokens[len(tokens)-1].operator == ";" {
		tokens = tokens[:len(tokens)-1]
	}

	var words []commandWord
	for _, token := range tokens {
		switch {
		case token.kind == policyTokenWord && literalPolicyWord(token.word):
			words = append(words, token.word)
		case token.operator == "&&":
			if !logicalCWDKnown {
				return policyGitInvocation{}, false
			}
			base = literalPolicyCDTarget(words, base)
			if base == "" {
				return policyGitInvocation{}, false
			}
			words = nil
		case token.operator == "\n" && len(words) == 0:
			continue
		default:
			return policyGitInvocation{}, false
		}
	}
	if len(words) < 2 || (words[0].value != "git" && words[0].value != "/usr/bin/git") {
		return policyGitInvocation{}, false
	}
	args := words[1:]
	for len(args) > 0 {
		option := args[0].value
		switch {
		case option == "-C" && len(args) > 1 && literalPolicyPath(args[1]):
			args = args[2:]
		case strings.HasPrefix(option, "-C") && option != "-C":
			path := args[0]
			path.value = option[2:]
			if !literalPolicyPath(path) {
				return policyGitInvocation{}, false
			}
			args = args[1:]
		case slices.Contains([]string{"--no-pager", "-p", "--paginate", "--literal-pathspecs"}, option):
			args = args[1:]
		case strings.HasPrefix(option, "-"):
			return policyGitInvocation{}, false
		default:
			return parsePolicyGitInvocation(words[1:], base)
		}
	}
	return policyGitInvocation{}, false
}

func literalPolicyWord(word commandWord) bool {
	return word.static() && !word.tilde
}

// literalPolicyCDTarget は子 Bash が引き継ぐ論理パスと物理パスが一致する cd だけを追う。
func literalPolicyCDTarget(words []commandWord, base string) string {
	if len(words) < 2 || words[0].value != "cd" || !literalPolicyWord(words[0]) {
		return ""
	}
	args := words[1:]
	if len(args) == 2 && args[0].value == "--" && literalPolicyWord(args[0]) {
		args = args[1:]
	}
	if len(args) != 1 || strings.HasPrefix(args[0].value, "-") || !literalPolicyPath(args[0]) || base == "" {
		return ""
	}
	value := args[0].value
	if !filepath.IsAbs(value) {
		value = filepath.Join(base, value)
	}
	if resolved := resolvePolicyDirectory(args[0], base); resolved == filepath.Clean(value) {
		return resolved
	}
	return ""
}

// 途中に .. があるパスは、前の成分が symlink のときに論理パスと物理パスで実行先が異なり得る。
func literalPolicyPath(word commandWord) bool {
	if !literalPolicyWord(word) || word.value == "" {
		return false
	}
	value := word.value
	for strings.HasPrefix(value, "../") {
		value = strings.TrimPrefix(value, "../")
	}
	return value == ".." || !slices.Contains(strings.Split(value, "/"), "..")
}

// policyBashEnvironmentKnown は起動ファイルや継承した設定がコマンドと実行先を変え得る場合を除く。
func policyBashEnvironmentKnown() bool {
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		if !policyBashEnvironmentEntryKnown(name, value) {
			return false
		}
	}
	return true
}

func policyBashEnvironmentEntryKnown(name, value string) bool {
	if gitx.StripsInheritedEnv(name) || strings.HasPrefix(name, "BASH_FUNC_") || strings.HasPrefix(value, "() {") {
		return false
	}
	switch name {
	case "BASH_ENV", "ENV", "SHELLOPTS", "BASHOPTS", "CDPATH":
		return value == ""
	}
	return true
}
