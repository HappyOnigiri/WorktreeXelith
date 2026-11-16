package daemon

import (
	"fmt"
	"strings"
	"testing"

	"github.com/HappyOnigiri/WorktreeX/internal/diag"
	"github.com/HappyOnigiri/WorktreeX/internal/discovery"
	"github.com/HappyOnigiri/WorktreeX/internal/domain"
	"github.com/HappyOnigiri/WorktreeX/internal/workspace"
)

// size が一致する候補を数えた結果は、候補の数とともに利用者へ示す。
// 1 件の候補で負数にならないことを境界として固定する。
func TestLFSObjectFindingCountsRepairableCandidates(t *testing.T) {
	t.Parallel()
	repo := discovery.Repository{MainPath: domain.CanonicalPath("/source")}
	finding := lfsObjectFinding(repo, workspace.LFSObjectDiagnostics{Objects: []workspace.LFSObjectDiagnostic{{
		Object: workspace.LFSObjectInfo{OID: "sha256:abc", Size: 10}, CandidatePath: "asset.bin",
	}}}, 0)
	if finding.Severity != diag.SeverityInfo {
		t.Fatalf("finding=%+v, want a repairable informational finding", finding)
	}
	if !strings.HasPrefix(finding.Cause, "1 LFS object(s),") {
		t.Fatalf("cause=%q, want one repairable object", finding.Cause)
	}
}

func TestLFSObjectFindingReportsRepairableCandidatesAsInfo(t *testing.T) {
	repo := discovery.Repository{MainPath: domain.CanonicalPath("/source")}
	finding := lfsObjectFinding(repo, workspace.LFSObjectDiagnostics{Objects: []workspace.LFSObjectDiagnostic{{
		Object: workspace.LFSObjectInfo{OID: "sha256:abc", Size: 10}, CandidatePath: "asset.bin",
	}}}, 0)
	if finding.Check != diag.CheckLFSObjects || finding.Severity != diag.SeverityInfo {
		t.Fatalf("finding=%+v", finding)
	}
	if finding.Messages.Summary.ID == "" || finding.Messages.Cause.ID == "" || finding.Messages.Action.ID == "" {
		t.Fatalf("finding messages=%+v", finding.Messages)
	}
}

func TestLFSObjectFindingReportsUnrepairableObjectAsProblem(t *testing.T) {
	repo := discovery.Repository{MainPath: domain.CanonicalPath("/source")}
	finding := lfsObjectFinding(repo, workspace.LFSObjectDiagnostics{Objects: []workspace.LFSObjectDiagnostic{{
		Object: workspace.LFSObjectInfo{OID: "sha256:abc", Size: 10},
	}}}, 0)
	if finding.Check != diag.CheckLFSObjects || finding.Severity != diag.SeverityProblem {
		t.Fatalf("finding=%+v", finding)
	}
	if finding.Messages.Summary.ID == "" || finding.Messages.Cause.ID == "" || finding.Messages.Action.ID == "" {
		t.Fatalf("finding messages=%+v", finding.Messages)
	}
}

// 欠落が大量にある repository でも、detail は上限件数と省略件数の行だけにして RPC frame へ収める。
// Cause の件数は省略せず全 object を数える。
func TestLFSObjectFindingCapsObjectDetails(t *testing.T) {
	t.Parallel()
	repo := discovery.Repository{MainPath: domain.CanonicalPath("/source")}
	objects := make([]workspace.LFSObjectDiagnostic, 0, maxLFSObjectDetails+5)
	for index := range maxLFSObjectDetails + 5 {
		objects = append(objects, workspace.LFSObjectDiagnostic{Object: workspace.LFSObjectInfo{OID: fmt.Sprintf("sha256:%064d", index), Size: 10}})
	}
	finding := lfsObjectFinding(repo, workspace.LFSObjectDiagnostics{Objects: objects}, 0)
	if len(finding.Details) != maxLFSObjectDetails+1 || len(finding.Messages.Details) != maxLFSObjectDetails+1 {
		t.Fatalf("details=%d messages=%d, want %d object details and one omission line", len(finding.Details), len(finding.Messages.Details), maxLFSObjectDetails)
	}
	if last := finding.Details[len(finding.Details)-1]; last != "5 more LFS object(s) omitted" {
		t.Fatalf("last detail=%q, want the omitted count", last)
	}
	if !strings.HasPrefix(finding.Cause, fmt.Sprintf("%d of %d ", maxLFSObjectDetails+5, maxLFSObjectDetails+5)) {
		t.Fatalf("cause=%q, want every missing object counted", finding.Cause)
	}
}

// fetch 設定で除外した path は欠落として数えず、OK の detail に件数だけを示す。
func TestLFSObjectOKFindingReportsFetchExcludedPaths(t *testing.T) {
	t.Parallel()
	repo := discovery.Repository{MainPath: domain.CanonicalPath("/source")}
	finding := lfsObjectOKFinding(repo, 0, 3)
	if finding.Severity != diag.SeverityOK || len(finding.Details) != 2 || len(finding.Messages.Details) != 2 {
		t.Fatalf("finding=%+v, want the checked count and the excluded count", finding)
	}
	if !strings.HasPrefix(finding.Details[1], "3 LFS path(s) excluded") {
		t.Fatalf("details=%v", finding.Details)
	}
}
