package dashboard

import "strings"

func (m model) commandLines(action Action, width int) []string {
	lines := []string{dim + m.t("dashboard.command") + reset}
	for _, line := range wrap(formatCommand(action), width) {
		lines = append(lines, dim+line+reset)
	}
	return append(lines, "")
}

// formatCommand は対象ディレクトリと引数を shell で再現できる形にする。
// 表示専用であり、実行には元の Action を使う。
func formatCommand(action Action) string {
	parts := []string{"wx"}
	for _, arg := range action.Args {
		parts = append(parts, quoteCommandArg(arg))
	}
	command := strings.Join(parts, " ")
	if action.WorkDir != "" {
		command = "cd " + quoteCommandArg(action.WorkDir) + " && " + command
	}
	return command
}

func quoteCommandArg(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r))
	}) < 0 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
