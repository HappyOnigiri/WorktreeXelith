# Worktree Xelith

[![Worktree Xelith 介绍视频](.github/readme-hero-en.gif)](.github/worktree-xelith-en.mp4)

[English](README.md) | [日本語](README.ja.md) | 简体中文

只需一条命令，即可在独立的 Git worktree 中启动 Claude Code 或 Codex。
Worktree Xelith（`wx`）负责在 macOS 上准备和管理工作环境，让你无需切换原仓库的分支，就能把任务交给智能体。

## 特性

- **独立的工作环境** — 智能体在 detached HEAD 状态的 Git worktree 中工作，不会更改原工作区的 HEAD、暂存区和已跟踪文件。由于不占用分支，你仍可在原仓库中切换到同一分支。
- **随时开始工作** — 可以选择预先准备工作环境的 Hot Standby，或按需创建的 Cold Start。
- **节省磁盘空间** — 利用 APFS 的 Copy on Write，与原工作区中内容相同的文件共享数据，减少 worktree 的磁盘占用。
- **开发环境设置** — 首次使用时，可让智能体配置文件复制、共享链接和准备步骤，再通过构建与测试验证环境。
- **支持多个仓库** — 从父目录启动，即可将其下各仓库的 worktree 一起准备在同一个 workspace 中。
- **自动保存与清理** — 智能体正常退出后，wx 会将工作状态保存为快照，并按各自的保留设置清理 worktree 和快照。
- **熟悉的命令** — 直接使用 Claude Code 或 Codex 的常用参数，也可以指定起始分支。

## 安装

需要 **搭载 Apple Silicon 的 macOS**、**Git**，以及 **Claude Code 或 Codex**。

```sh
curl -fsSL https://github.com/HappyOnigiri/WorktreeXelith/releases/latest/download/install.sh | bash
```

安装后会打开交互式设置，可配置智能体的 hook、后台守护进程等。

## 快速开始

在你要处理的仓库中运行：

```sh
wx claude
wx codex
```

所选智能体会在 wx 管理的 worktree 中启动。
首次在仓库中启动时，选择准备策略并检查环境。确认结果后，可让智能体完成 Git 未跟踪文件的配置和仓库所需的准备步骤。

`claude` 或 `codex` 后面的参数会原样传递给智能体，但在 `--` 之前的
`--branch <branch|repo=branch>` 或 `--branch=<branch|repo=branch>` 会由 wx 处理。
要指定起始分支，可以将 wx 选项放在智能体名称之前或之后：

```sh
wx --branch feature/api codex
wx codex --branch feature/api
```

Claude Code 和 Codex 按启动路径管理会话日志，因此 worktree 发生变化后，标准的 continue / resume 命令可能难以找到过去的会话。
wx 对这些命令进行封装，提供用于选择并恢复过去会话的 UI。

```sh
wx claude --resume
wx codex resume
```

如果保存的工作状态仍可用，wx 会将其恢复到继续使用的会话中。快照包含提交、已暂存和未暂存的更改，以及 Git 未忽略的未跟踪文件。
快照的保留期限和保存范围有限；需要长期保留的成果，请提交并 push。

## 更多功能

- **管理界面：** 不带参数运行 `wx`，即可查看 workspace 和 worktree 的状态。
- **运行其他工具：** `wx shell` 可启动 shell，`wx run <command>` 可运行任意命令；它们在受管理的 worktree 中运行，退出时自动归还 worktree。
- **状态与诊断：** `wx status`、`wx doctor`。
- **会话管理与清理：** `wx slots`、`wx resume`、`wx gc --dry-run`、`wx clear`。
- **配置：** 使用 `wx config` 查看配置或修改单个配置值。

命令和选项的详细用法，请参阅 `wx --help` 和 `wx <command> --help`。

## 卸载

```sh
curl -fsSL https://github.com/HappyOnigiri/WorktreeXelith/releases/latest/download/uninstall.sh | bash
```

删除 wx 管理的 worktree（包括未保存的工作）、hook 条目、LaunchAgent、配置文件和可执行文件。

## 参与贡献

欢迎参与贡献！
你可以通过 [Issues](https://github.com/HappyOnigiri/WorktreeXelith/issues) 报告问题或提出想法，也可以提交 [Pull Request](https://github.com/HappyOnigiri/WorktreeXelith/pulls)。
同样欢迎改进文档和翻译。
