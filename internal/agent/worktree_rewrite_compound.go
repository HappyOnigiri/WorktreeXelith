package agent

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// compoundCommand は連結を区切りで分けた 1 つの単純コマンドである。
// before と after は前後の繋ぎ方で、先頭と末尾は空、それ以外は区切りの原文か `|`・`&` である。
// opaque は入れ子や heredoc があり、語だけでは内容を追えないことを表す。
type compoundCommand struct {
	words         []policyToken
	before, after string
	opaque        bool
	redirect      bool
}

func (c compoundCommand) plain() bool {
	return !c.opaque && !c.redirect
}

// shellReservedWords は制御構文を始める語で、続く git worktree add が条件付きで走ることを表す。
var shellReservedWords = map[string]bool{
	"if": true, "then": true, "elif": true, "else": true, "fi": true, "for": true, "while": true, "until": true,
	"do": true, "done": true, "case": true, "esac": true, "select": true, "function": true, "{": true, "}": true, "!": true,
}

// compoundUnsafeCommands は実行先や実行内容を静的に追えなくするコマンドで、連結のどこにあっても書き換えない。
// `env sh -c` や `sudo -u x bash` のような包む語を読み飛ばすと引数の形を取りこぼすので、先頭に限らずどの語にあっても拒否する。
// `.` だけは `ls .` のような引数と区別できないため、先頭の語に限る。
// commentlint:allow-long -- どの語でも拒否する理由と、`.` の例外を残す
var compoundUnsafeCommands = map[string]bool{
	"eval": true, "source": true, "exec": true, "pushd": true, "popd": true,
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "xargs": true,
}

// compoundPathVariable は書き換えでパスを受け取る変数参照（`$WT` と `${WT}`）である。
var compoundPathVariable = regexp.MustCompile(`^\$(?:([A-Za-z_][A-Za-z0-9_]*)|\{([A-Za-z_][A-Za-z0-9_]*)\})$`)

// compoundReservedVariables は代入すると後続のコマンドや shell 自体の振る舞いが変わる変数である。
var compoundReservedVariables = map[string]bool{
	"PATH": true, "HOME": true, "PWD": true, "OLDPWD": true, "IFS": true, "CDPATH": true, "SHELL": true, "TMPDIR": true,
}

const (
	compoundLiteralPathVariable = "wx_worktree_path"
	compoundCDPATHGuard         = `case "${CDPATH-}" in (*[!:]*) printf '%s\n' 'wx: CDPATH may change the cd destination; retry with an absolute or ./-prefixed cd path.' >&2; exit 1;; esac; `
)

// rewriteCompoundWorktreeAdd は連結の中の git worktree add 1 つを wx new へ写す。
// 変数は貸出先で上書きし、固定パスは直後の cd と一緒に置き換える。
// 前にある cd は追い、workspace 内に解決できる場合だけ受け付ける。
func rewriteCompoundWorktreeAdd(command string, location worktreeAddLocation) (string, bool) {
	tokens, ok := lexPolicyCommand(command)
	if !ok {
		return "", false
	}
	if candidates, unsafe := scanCompoundTokens(tokens); candidates != 1 || unsafe {
		return "", false
	}
	commands, ok := splitCompoundCommands(tokens)
	if !ok {
		return "", false
	}
	walk := compoundWalk{directory: location.cwd, root: location.root}
	for index, current := range commands {
		if !walk.admit(current) {
			return "", false
		}
		words := policyShellWords(current.words)
		candidate, isCandidate := gitWorktreeAddArguments(words)
		if !isCandidate {
			if !walk.follow(words, current) {
				return "", false
			}
			continue
		}
		switch current.before {
		case "", ";", "&&", "\n":
		default:
			return "", false
		}
		if !current.plain() || (current.after != "" && current.after != "&&") {
			return "", false
		}
		verdict := rewriteWorktreeAddCandidate(candidate, worktreeAddLocation{cwd: walk.directory, root: location.root})
		if verdict.decision != worktreeAddRewrite {
			return "", false
		}
		if len(candidate.directories) == 0 && (walk.moved || !verdict.path.expanded) && !walk.inRepository() {
			return "", false
		}
		return compoundReplacement(command, verdict, current, commands[index+1:], walk.directory)
	}
	return "", false
}

// scanCompoundTokens は置換の本文まで含めて git worktree add の数を数え、実行内容を静的に追えなくするコマンドの有無を返す。
// allow で返す command に、書き換えなかった git worktree add が残らないようにするためである。
// `env git worktree add` や `nohup git worktree add` も逃さないよう、語の並びに `worktree add` があれば候補に数える。
// commentlint:allow-long -- 本文まで数える理由と、候補を広く数える理由を残す
func scanCompoundTokens(tokens []policyToken) (candidates int, unsafe bool) {
	var words []shellWord
	flush := func() {
		for index := range words {
			if index+1 < len(words) && words[index].value == "worktree" && words[index+1].value == "add" {
				candidates++
				break
			}
		}
		first := 0
		for first < len(words) && isAssignmentWord(words[first].value) {
			first++
		}
		for index := first; index < len(words); index++ {
			name := filepath.Base(words[index].value)
			if compoundUnsafeCommands[name] || (index == first && name == ".") {
				unsafe = true
			}
		}
		words = nil
	}
	for _, token := range tokens {
		if token.kind != policyTokenWord {
			flush()
			continue
		}
		for _, body := range token.word.substitutions {
			bodyCandidates, bodyUnsafe := scanCompoundTokens(body)
			candidates += bodyCandidates
			unsafe = unsafe || bodyUnsafe
		}
		words = append(words, policyShellWord(token.word))
	}
	flush()
	return candidates, unsafe
}

// splitCompoundCommands は token 列を単純コマンドへ分ける。候補より後ろの構文は問わないが、
// 括弧が現れた後は候補が入れ子の中にあり得るので、以降のコマンドを opaque にする。
// 引用されていない `$(...)` の中身は外側のコマンドに数えず、それを含むコマンドを opaque にする。
func splitCompoundCommands(tokens []policyToken) ([]compoundCommand, bool) {
	var commands []compoundCommand
	var current compoundCommand
	nested := false
	substitution := 0
	closeCommand := func(after string) {
		// && の直後の改行は同じ連結を続ける。
		if after == "\n" && current.before == "&&" && len(current.words) == 0 && current.plain() {
			return
		}
		current.after = after
		if len(current.words) > 0 {
			commands = append(commands, current)
		}
		current = compoundCommand{before: after, opaque: nested}
	}
	for _, token := range tokens {
		if substitution > 0 {
			if token.kind == policyTokenOpen {
				substitution++
			} else if token.kind == policyTokenClose {
				substitution--
			}
			continue
		}
		if token.kind == policyTokenOpen && token.operator == "$(" {
			substitution++
			current.opaque = true
			continue
		}
		switch token.kind {
		case policyTokenWord:
			current.words = append(current.words, token)
		case policyTokenSeparator:
			closeCommand(token.operator)
		case policyTokenPipe:
			closeCommand("|")
		case policyTokenBackground:
			closeCommand("&")
		case policyTokenOpen, policyTokenClose:
			nested = true
			current.opaque = true
		case policyTokenRedirect:
			current.redirect = true
			current.opaque = current.opaque || strings.HasPrefix(token.operator, "<<")
		}
	}
	closeCommand("")
	return commands, len(commands) > 0
}

// compoundWalk は候補より前のコマンドを順に読み、候補が走るディレクトリを追う。
type compoundWalk struct {
	directory, root, canonicalRoot string
	// moved は cd で cwd から移ったこと、conditional は最後の cd が `&&` の後にあり走らない場合があることを表す。
	moved, conditional bool
}

// inRepository は作成コマンドの実行先が workspace 内のリポジトリにあるかを返す。
func (w compoundWalk) inRepository() bool {
	if w.moved {
		return insideRepository(w.directory, w.canonicalRoot)
	}
	directory, dirErr := filepath.EvalSymlinks(w.directory)
	root, rootErr := filepath.EvalSymlinks(w.root)
	return dirErr == nil && rootErr == nil && withinDirectory(root, directory) && insideRepository(directory, root)
}

// admit はコマンドの前の繋ぎ方が、追っているディレクトリを不確かにしないかを返す。
// `&&` の後の cd は前が失敗すると走らないので、その後に `;` や改行で続くコマンドは cd の有無が分からない。
func (w *compoundWalk) admit(current compoundCommand) bool {
	if !w.moved {
		return true
	}
	switch current.before {
	case "&&", "|":
		return true
	case ";", "\n":
		return !w.conditional
	default:
		return false
	}
}

// follow は候補より前のコマンド 1 つを読む。実行先を変えるコマンドのうち、追えるのは引数 1 つの cd だけである。
func (w *compoundWalk) follow(words []shellWord, current compoundCommand) bool {
	index := 0
	for index < len(words) && isAssignmentWord(words[index].value) {
		index++
	}
	if index >= len(words) {
		return true
	}
	if shellReservedWords[words[index].value] {
		return false
	}
	if words[index].value == "command" {
		index++
		for index < len(words) && words[index].value == "-p" {
			index++
		}
		if index < len(words) && words[index].value == "--" {
			index++
		}
	}
	if index == len(words) || filepath.Base(words[index].value) != "cd" {
		return true
	}
	// パイプの中の cd は別プロセスで走り、`||` の後の cd は走るとは限らない。
	if !current.plain() || len(words) != index+2 || current.after == "|" || current.after == "&" || current.before == "||" || current.before == "|" {
		return false
	}
	target := words[index+1]
	if w.directory == "" && !filepath.IsAbs(target.value) && !target.tilde {
		return false
	}
	resolved := resolvePolicyDirectory(commandWord{value: target.value, quoted: target.quoted, expanded: target.expanded, globbed: target.globbed, tilde: target.tilde}, w.directory)
	if resolved == "" || w.root == "" {
		return false
	}
	canonicalRoot, err := filepath.EvalSymlinks(w.root)
	if err != nil || !withinDirectory(canonicalRoot, resolved) {
		return false
	}
	w.directory, w.canonicalRoot, w.moved, w.conditional = resolved, canonicalRoot, true, current.before == "&&"
	return true
}

// compoundReplacement は貸出先を表示し、固定パスの場合は続く cd の行き先にも渡す。
func compoundReplacement(command string, verdict worktreeAddVerdict, current compoundCommand, following []compoundCommand, cwd string) (string, bool) {
	start := current.words[0].start
	end := current.words[len(current.words)-1].end
	var name, changeDirectory, leaseGuard string
	if verdict.path.expanded {
		match := compoundPathVariable.FindStringSubmatch(verdict.path.value)
		if match == nil {
			return "", false
		}
		name = match[1] + match[2]
		if compoundReservedVariables[name] {
			return "", false
		}
	} else {
		name = compoundLiteralPathVariable
		if current.after != "&&" || strings.Contains(command, name) || !literalWorktreeConsumer(verdict, following, cwd) {
			return "", false
		}
		words := following[0].words
		if strings.Trim(command[end:words[0].start], " \t\n") != "&&" {
			return "", false
		}
		end = words[len(words)-1].end
		target := words[len(words)-1].word.value
		// cd が CDPATH を参照する形では、貸出前に実行時の値を確かめる。
		if !filepath.IsAbs(target) && !strings.HasPrefix(target, "./") {
			leaseGuard = compoundCDPATHGuard
		}
		changeDirectory = ` && cd -- "$` + name + `"`
	}
	inner := verdict.command
	if verdict.directory != "" {
		inner = "cd " + shellSingleQuote(verdict.directory) + " && " + inner
	}
	lease := name + "=$(" + leaseGuard + inner + `) && echo "` + name + `=$` + name + `"`
	return command[:start] + lease + changeDirectory + command[end:], true
}

// literalWorktreeConsumer は固定パスを直後の cd だけが消費し、貸出に失敗すれば後続が走らないことを確認する。
func literalWorktreeConsumer(verdict worktreeAddVerdict, following []compoundCommand, cwd string) bool {
	if len(following) == 0 {
		return false
	}
	cd := following[0]
	if cd.before != "&&" || !cd.plain() || (cd.after != "" && cd.after != "&&") || cd.words[0].word.value != "cd" {
		return false
	}
	args := cd.words[1:]
	if len(args) == 2 && args[0].word.value == "--" {
		args = args[1:]
	}
	if len(args) != 1 || strings.HasPrefix(args[0].word.value, "-") {
		return false
	}
	gitCWD := cwd
	if verdict.directory != "" {
		gitCWD = verdict.directory
	}
	target := literalWorktreePath(verdict.path, gitCWD)
	if target == "" || target != literalWorktreePath(policyShellWord(args[0].word), cwd) {
		return false
	}
	return literalWorktreeSuffix(following[1:], filepath.Base(target))
}

// literalWorktreePath は存在しない作成先も比べるため、親の symlink によって意味が変わる .. を除いて比較する。
func literalWorktreePath(word shellWord, cwd string) string {
	if word.value == "" || word.expanded || word.globbed || word.tilde {
		return ""
	}
	for _, component := range strings.Split(word.value, "/") {
		if component == ".." {
			return ""
		}
	}
	path := filepath.Clean(word.value)
	if path == "." || path == "/" {
		return ""
	}
	if !filepath.IsAbs(path) {
		if cwd == "" {
			return path
		}
		path = filepath.Join(cwd, path)
	}
	parent := resolveLiteralWorktreePath(filepath.Dir(path))
	if parent == "" {
		return ""
	}
	return filepath.Join(parent, filepath.Base(path))
}

// resolveLiteralWorktreePath は実在する祖先の実パスに、未作成の末尾を付け直す。
func resolveLiteralWorktreePath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	if !errors.Is(err, os.ErrNotExist) || path == filepath.Dir(path) {
		return ""
	}
	// リンク切れのパスを未作成のディレクトリと同じように扱わない。
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return ""
	}
	parent := resolveLiteralWorktreePath(filepath.Dir(path))
	if parent == "" {
		return ""
	}
	return filepath.Join(parent, filepath.Base(path))
}

// literalWorktreeSuffix は貸出先からの相対操作を残す。元の作成先を参照し得る語や展開があれば拒否する。
// パイプは連結全体が cd の成功を待つが、改行やセミコロンは失敗時も次のコマンドを走らせる。
func literalWorktreeSuffix(commands []compoundCommand, basename string) bool {
	originalPath, err := regexp.Compile(`(?i)(^|[^[:alnum:]_.-])(?:` + regexp.QuoteMeta(basename) + `|\.\.)([^[:alnum:]_.-]|$)`)
	if err != nil {
		return false
	}
	for _, command := range commands {
		if command.opaque || shellReservedWords[command.words[0].word.value] {
			return false
		}
		switch command.after {
		case "", "&&", "|":
		default:
			return false
		}
		for _, token := range command.words {
			word := token.word
			if !word.static() || word.tilde || originalPath.MatchString(word.value) {
				return false
			}
		}
	}
	return true
}

func policyShellWords(tokens []policyToken) []shellWord {
	words := make([]shellWord, 0, len(tokens))
	for _, token := range tokens {
		words = append(words, policyShellWord(token.word))
	}
	return words
}

func policyShellWord(word commandWord) shellWord {
	return shellWord{value: word.value, expanded: word.expanded, quoted: word.quoted, globbed: word.globbed, tilde: word.tilde}
}
