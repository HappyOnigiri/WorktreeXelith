package dashboard

import (
	"context"
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/HappyOnigiri/WorktreeX/internal/config"
	"github.com/HappyOnigiri/WorktreeX/internal/setup"
)

func TestCommandDescriptions(t *testing.T) {
	for _, language := range []string{config.LanguageEnglish, config.LanguageJapanese} {
		cfg := config.Defaults()
		cfg.System.Language = language
		m := newModel(context.Background(), Options{Config: cfg, Setup: []setup.Step{hooksStep()}})
		for tab, items := range tabMenus {
			m.tab = tab
			for index, item := range items {
				m.selected = index
				if tab == 5 {
					m.selected += len(m.setupItems())
				}
				text := xansi.Strip(strings.Join(m.descriptionLines(120), "\n"))
				want := strings.Join(append([]string{"wx", item.command}, item.defaultArgs...), " ")
				if !strings.Contains(text, want) {
					t.Errorf("tab %d item %d omitted %q: %s", tab, index, want, text)
				}
			}
		}
		m.tab, m.selected = 5, 0
		if text := strings.Join(m.descriptionLines(120), "\n"); !strings.Contains(text, "wx setup --item hooks.claude") {
			t.Fatalf("setup command missing: %s", text)
		}
		m.tab, m.settingsOpen, m.settingsEnv, m.selected = 2, true, 0, 0
		if text := strings.Join(m.descriptionLines(120), "\n"); !strings.Contains(text, "wx config --system "+m.configItems()[0].Key) {
			t.Fatalf("config command missing: %s", text)
		}
	}
}

func TestConfirmCommandIncludesSelectedArguments(t *testing.T) {
	m := newModel(context.Background(), Options{Config: config.Defaults()})
	m.tab, m.width = 4, 160
	m.pending = tabMenus[4][1]
	m.input = "--standby --dry-run"
	text := xansi.Strip(strings.Join(m.confirmView(), "\n"))
	if !strings.Contains(text, "wx clear --standby --dry-run") {
		t.Fatalf("confirmation omitted selected flags: %s", text)
	}
	if len(m.result.Args) != 0 {
		t.Fatalf("rendering changed result: %+v", m.result)
	}
}

func TestFormatCommandQuotesShellArguments(t *testing.T) {
	action := Action{WorkDir: "/tmp/work space", Args: []string{"config", "--system", "key", "a'b $(touch file)"}}
	want := "cd '/tmp/work space' && wx config --system key 'a'\"'\"'b $(touch file)'"
	if got := formatCommand(action); got != want {
		t.Fatalf("command=%q, want %q", got, want)
	}
	if got := quoteCommandArg(""); got != "''" {
		t.Fatalf("empty argument=%q", got)
	}
}
