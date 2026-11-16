package workspace

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/HappyOnigiri/WorktreeX/internal/discovery"
)

// LFSFetchFilter は git-lfs が smudge で実体化する path を絞る lfs.fetchinclude/fetchexclude の値である。
// 値は git-lfs と同じくカンマ区切りの pattern 列で、wx は解釈せず git-lfs へそのまま渡す。
type LFSFetchFilter struct {
	Include string
	Exclude string
}

// Empty は fetch 設定が無く、全 LFS path が smudge の対象になるかを返す。
func (f LFSFetchFilter) Empty() bool { return f.Include == "" && f.Exclude == "" }

// LFSFetchFilterConfig は git config（system・global・local）にある fetch 設定だけを読む。
// .lfsconfig は要求 OID ごとに異なるため見積りが tree から読み、この値は見積り cache の key に使う。
func (p *Preparer) LFSFetchFilterConfig(ctx context.Context, repo discovery.Repository) (LFSFetchFilter, error) {
	var filter LFSFetchFilter
	for _, item := range []struct {
		key   string
		value *string
	}{{"lfs.fetchinclude", &filter.Include}, {"lfs.fetchexclude", &filter.Exclude}} {
		result, err := p.capacityGit(ctx, repo, nil, "config", "--default", "", "--get", item.key)
		if err != nil {
			return LFSFetchFilter{}, fmt.Errorf("read %s for capacity estimate: %w", item.key, err)
		}
		*item.value = strings.TrimSpace(result.Stdout)
	}
	return filter, nil
}

// lfsFetchFilter は checkout 時に git-lfs が使う fetch 設定を組み立てる。
// git-lfs と同じく key ごとに git config を優先し、無い key だけ要求 OID の .lfsconfig から補う。
func (p *Preparer) lfsFetchFilter(ctx context.Context, repo discovery.Repository, oid string, entries []capacityTreeEntry) (LFSFetchFilter, error) {
	filter, err := p.LFSFetchFilterConfig(ctx, repo)
	if err != nil {
		return LFSFetchFilter{}, err
	}
	hasLFSConfig := slices.ContainsFunc(entries, func(entry capacityTreeEntry) bool { return entry.Path == ".lfsconfig" })
	if !hasLFSConfig {
		return filter, nil
	}
	for _, item := range []struct {
		key   string
		value *string
	}{{"lfs.fetchinclude", &filter.Include}, {"lfs.fetchexclude", &filter.Exclude}} {
		if *item.value != "" {
			continue
		}
		result, err := p.capacityGit(ctx, repo, nil, "config", "--blob", oid+":.lfsconfig", "--default", "", "--get", item.key)
		if err != nil {
			return LFSFetchFilter{}, fmt.Errorf("read %s from .lfsconfig for capacity estimate: %w", item.key, err)
		}
		*item.value = strings.TrimSpace(result.Stdout)
	}
	return filter, nil
}

// lfsFetchExcludedPaths は fetch 設定により smudge で pointer のまま書かれる LFS path を返す。
// pattern の照合は git-lfs 自身の ls-files に任せ、wx は gitignore 形式の解釈を持たない。
// ls-files は include/exclude を明示した場合だけ絞り込むので、fetch 設定の有無に左右されない。
func (p *Preparer) lfsFetchExcludedPaths(ctx context.Context, repo discovery.Repository, oid string, entries []capacityTreeEntry, lfsPaths map[string]bool) (map[string]bool, error) {
	if len(lfsPaths) == 0 {
		return nil, nil
	}
	filter, err := p.lfsFetchFilter(ctx, repo, oid, entries)
	if err != nil {
		return nil, err
	}
	if filter.Empty() {
		return nil, nil
	}
	args := []string{"lfs", "ls-files", "--name-only"}
	if filter.Include != "" {
		args = append(args, "--include="+filter.Include)
	}
	if filter.Exclude != "" {
		args = append(args, "--exclude="+filter.Exclude)
	}
	listing, err := p.capacityGit(ctx, repo, nil, append(args, oid)...)
	if err != nil {
		return nil, fmt.Errorf("list LFS paths allowed by the fetch filter: %w", err)
	}
	included := make(map[string]bool)
	for line := range strings.SplitSeq(listing.Stdout, "\n") {
		if line != "" {
			included[line] = true
		}
	}
	excluded := make(map[string]bool, len(lfsPaths))
	for path := range lfsPaths {
		if !included[path] {
			excluded[path] = true
		}
	}
	return excluded, nil
}
