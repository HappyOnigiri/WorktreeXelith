# Worktree Xelith

![Worktree Xelith logo against a tree and clouds](.github/readme-hero.webp)

English | [日本語](README.ja.md) | [简体中文](README.zh-CN.md)

Run Claude Code or Codex in a separate Git worktree with a single command.
Worktree Xelith (`wx`) prepares and manages workspaces on macOS, so you can hand off a task without switching branches in your source repository.

## Features

- **Separate workspaces** — Agents work in detached Git worktrees, keeping your source checkout's HEAD, index, and tracked files untouched.
  Detached HEAD also leaves branches free to check out in your source repository.
- **Ready when you need them** — A background daemon keeps workspaces ready with Hot Standby, or creates them on demand with Cold Start.
- **Save disk space** — APFS Copy on Write shares data for files with identical contents in the source checkout, reducing worktree disk usage.
- **Environment setup** — On first use, an agent can configure file copies, shared links, and preparation steps, then verify the environment with builds and tests.
- **Multiple repositories** — Launch from a parent directory to prepare worktrees for its repositories together in one workspace.
- **Automatic saving and cleanup** — wx snapshots work when an agent exits normally and cleans up worktrees and snapshots according to their retention settings.
- **Familiar commands** — Use Claude Code or Codex with their usual arguments, and optionally choose a starting branch.

## Installation

Requires **macOS on Apple Silicon**, **Git**, and **Claude Code or Codex**.

```sh
curl -fsSL https://github.com/HappyOnigiri/WorktreeXelith/releases/latest/download/install.sh | bash
```

The installer opens an interactive setup for settings such as agent hooks and the background daemon.

## Quick start

From the repository you want to work on:

```sh
wx claude
wx codex
```

Each command launches the chosen agent in a managed worktree.
On first use in a repository, choose a preparation policy and run the environment check.
You can then ask the agent to complete setup, including files Git does not track and repository-specific preparation steps.

Arguments after `claude` or `codex` are passed through unchanged, except that
`--branch <branch|repo=branch>` and `--branch=<branch|repo=branch>` can also be
placed after the agent name and before `--`.
To choose a starting branch, place the wx option before or after the agent name:

```sh
wx --branch feature/api codex
wx codex --branch feature/api
```

Claude Code and Codex associate session logs with the paths they run from, so their standard continue / resume commands can have trouble finding past sessions when the worktree changes.
wx wraps these commands with a UI for selecting and resuming past sessions.

```sh
wx claude --resume
wx codex resume
```

When saved work is available, wx restores it for the resumed session. Snapshots include commits, staged and unstaged changes, and untracked files that Git does not ignore.
Snapshots have retention limits and do not cover every file; commit and push work you want to keep permanently.

## More options

- **Management UI:** Run `wx` without arguments to view workspaces and their worktrees.
- **Other tools:** `wx shell` opens a shell and `wx run <command>` runs any command in a managed worktree; the worktree is returned automatically on exit.
- **Status and diagnostics:** `wx status`, `wx doctor`.
- **Sessions and cleanup:** `wx slots`, `wx resume`, `wx gc --dry-run`, `wx clear`.
- **Configuration:** `wx config` shows settings and can update individual values.

See `wx --help` and `wx <command> --help` for commands and options.

## Uninstallation

```sh
curl -fsSL https://github.com/HappyOnigiri/WorktreeXelith/releases/latest/download/uninstall.sh | bash
```

Removes wx-managed worktrees (including unsaved work), hook entries, the LaunchAgent, configuration files, and the executable.

## Contributing

Contributions are welcome!
Share bug reports and ideas through [Issues](https://github.com/HappyOnigiri/WorktreeXelith/issues), or send a [pull request](https://github.com/HappyOnigiri/WorktreeXelith/pulls).
Documentation improvements and translations are welcome too.
