package gitworktree

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAutomaticDefaultUsesLocalCommitsWithoutRefreshingRemote(t *testing.T) {
	git := requireGit(t)
	root := t.TempDir()
	repo := setupOriginClone(t, git, filepath.Join(root, "source"))
	runGit(t, git, repo, "commit", "--allow-empty", "-m", "local commit")
	localSHA := gitOutput(t, git, repo, "rev-parse", "refs/heads/main")
	if cached := gitOutput(t, git, repo, "rev-parse", "refs/remotes/origin/main"); cached == localSHA {
		t.Fatal("test requires local main to be ahead of origin/main")
	}
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(root, "worktrees"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
		if slices.Contains(args, "fetch") || slices.Contains(args, "ls-remote") {
			t.Error("automatic base resolution attempted to contact the remote")
		}
		return runCommand(ctx, binary, args...)
	}
	ctx := context.Background()
	for _, id := range []domain.SessionID{"cached", "uncached"} {
		if id == "uncached" {
			runGit(t, git, repo, "symbolic-ref", "--delete", "refs/remotes/origin/HEAD")
		}
		t.Run(string(id), func(t *testing.T) {
			base, err := ws.ResolveDefaultBranch(ctx, repo, "")
			if err != nil || base.BaseRef != "refs/heads/main" {
				t.Fatalf("resolve local main = %+v, err=%v", base, err)
			}
			info, err := ws.Create(ctx, ports.WorkspaceConfig{
				ProjectID: "proj", SessionID: id, Branch: "ao/" + string(id), FreshBranch: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if head := gitOutput(t, git, info.Path, "rev-parse", "HEAD"); info.BaseRef != base.BaseRef || info.BaseSHA != localSHA || head != localSHA {
				t.Fatalf("create worktree from local main = %+v, HEAD=%s, want SHA %s", info, head, localSHA)
			}
		})
	}
}
