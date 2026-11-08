# Worktree Xelith

[English](README.md) | 日本語 | [简体中文](README.zh-CN.md)

Claude Code や Codex を、コマンドひとつで専用の Git worktree に起動します。
Worktree Xelith（`wx`）が macOS 上の作業環境を準備・管理するので、元のリポジトリでブランチを切り替えずにエージェントへ作業を任せられます。

## 特長

- **作業環境を分離** — エージェントは detached な Git worktree で作業し、元の作業ツリーの HEAD・index・追跡ファイルを変更しません。ブランチを占有しないため、手元では同じブランチに切り替えられます。
- **すぐに作業を開始** — 事前に作業環境を用意する Hot Standby と、必要なときに作成する Cold Start を選べます。
- **ディスク容量を節約** — APFS の Copy on Write を活用し、元の作業ツリーと同じ内容のファイルのデータを共有して、worktree のディスク使用量を抑えます。
- **開発環境のセットアップ** — 初回はエージェントにファイルのコピー・共有リンク・準備手順の設定を任せ、ビルドやテストで検証できます。
- **複数リポジトリに対応** — 親ディレクトリから起動すると、配下のリポジトリの worktree を一つの workspace にまとめて準備します。
- **終了後の保存と整理** — 通常のエージェント終了時に作業をスナップショットへ保存し、worktree とスナップショットをそれぞれの保持設定に従って整理します。
- **いつものコマンドで操作** — Claude Code や Codex の引数をそのまま使え、開始元のブランチも指定できます。

## インストール

**Apple Silicon 搭載の macOS**、**Git**、および **Claude Code または Codex** が必要です。

```sh
curl -fsSL https://github.com/HappyOnigiri/WorktreeXelith/releases/latest/download/install.sh | bash
```

インストール後に対話セットアップが開き、エージェントの hook やバックグラウンドのデーモンなどを設定できます。

## 使い方

作業したいリポジトリで実行します。

```sh
wx claude
wx codex
```

選んだエージェントが、wx の管理する worktree で起動します。
リポジトリでの初回起動時に準備方針を選び、環境を検査します。結果を確認して、Git 管理外のファイルの配置やリポジトリ固有の準備をエージェントに任せられます。

`claude` または `codex` より後ろの引数は、`--` より前に置いた
`--branch <branch|repo=branch>` または `--branch=<branch|repo=branch>` を除き、そのままエージェントへ渡されます。
開始元のブランチを指定する wx のオプションは、エージェント名の前後どちらにも置けます。

```sh
wx --branch feature/api codex
wx codex --branch feature/api
```

Claude Code と Codex は起動パスごとにセッションログを管理するため、worktree が変わると標準の continue / resume では過去のセッションを見つけにくくなります。
wx はこれらをラップし、過去のセッションを選んで再開できる UI を提供します。

```sh
wx claude --resume
wx codex resume
```

保存した作業状態が利用できる場合は、再開するセッションへ復元します。スナップショットにはコミット、ステージ済み・未ステージの変更、Git の ignore 対象を除く未追跡ファイルが含まれます。
保持期間と保存対象には制限があるため、永続的に残したい成果はコミットして push してください。

## その他の機能

- **管理 UI:** 引数なしの `wx` で workspace と worktree の状況を確認できます。
- **ほかのツールの起動:** `wx shell` でシェル、`wx run <command>` で任意のコマンドを管理下の worktree 内で起動し、終了時に自動で返却します。
- **状態確認・診断:** `wx status`、`wx doctor`。
- **セッション管理・クリーンアップ:** `wx slots`、`wx resume`、`wx gc --dry-run`、`wx clear`。
- **設定:** `wx config` で設定の確認や個別の値の変更ができます。

コマンドやオプションの詳細は、`wx --help` と `wx <command> --help` を参照してください。

## アンインストール

```sh
curl -fsSL https://github.com/HappyOnigiri/WorktreeXelith/releases/latest/download/uninstall.sh | bash
```

wx が管理する worktree（未保存の作業を含む）・hook・LaunchAgent・設定ファイル・実行ファイルを削除します。

## コントリビュート

コントリビュートを歓迎します！
不具合報告やアイデアは [Issues](https://github.com/HappyOnigiri/WorktreeXelith/issues) へ、改善は [Pull Request](https://github.com/HappyOnigiri/WorktreeXelith/pulls) でお寄せください。
ドキュメントの改善や翻訳も歓迎です。
