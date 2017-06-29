package run

import (
	"bytes"
	"errors"
	"github.com/lesfurets/git-octopus/config"
	"github.com/lesfurets/git-octopus/git"
	"log"
)

type OctopusContext struct {
	Repo   *git.Repository
	Logger *log.Logger
}

const VERSION = "2.0"

func Run(context *OctopusContext, args ...string) error {

	octopusConfig, err := config.GetOctopusConfig(context.Repo, args)

	if err != nil {
		return err
	}

	if octopusConfig.PrintVersion {
		context.Logger.Println(VERSION)
		return nil
	}

	if len(octopusConfig.Patterns) == 0 {
		context.Logger.Println("Nothing to merge. No pattern given")
		return nil
	}

	status, _ := context.Repo.Git("status", "--porcelain")

	// This is not formally required but it would be an ambiguous behaviour to let git-octopus run on unclean state.
	if len(status) != 0 {
		return errors.New("The repository has to be clean.")
	}

	branchList := resolveBranchList(context.Repo, context.Logger, octopusConfig.Patterns, octopusConfig.ExcludedPatterns)

	if len(branchList) == 0 {
		return nil
	}

	initialHeadCommit, _ := context.Repo.Git("rev-parse", "HEAD")

	if !octopusConfig.DoCommit {
		defer context.Repo.Git("reset", "-q", "--hard", initialHeadCommit)
	}

	context.Logger.Println()

	parents, err := mergeHeads(context, branchList)

	if err != nil {
		return err
	}

	if !octopusConfig.DoCommit {
		return nil
	}

	if len(parents) == 1 {
		// This is either a fast-forward update or a no op
		context.Repo.Git("update-ref", "HEAD", parents[0])
	} else {
		// We need at least 2 parents to create a merge commit
		tree, _ := context.Repo.Git("write-tree")
		args := []string{"commit-tree"}
		for _, parent := range parents {
			args = append(args, "-p", parent)
		}
		args = append(args, "-m", octopusCommitMessage(branchList), tree)
		commit, _ := context.Repo.Git(args...)
		context.Repo.Git("update-ref", "HEAD", commit)
	}

	return nil
}

// The logic of this function is copied directly from git-merge-octopus.sh
func mergeHeads(context *OctopusContext, remotes []git.LsRemoteEntry) ([]string, error) {
	head, _ := context.Repo.Git("rev-parse", "--verify", "-q", "HEAD")

	mrc := []string{head}
	mrt, _ := context.Repo.Git("write-tree")
	nonFfMerge := false

	for _, lsRemoteEntry := range remotes {
		common, err := context.Repo.Git(append([]string{"merge-base", "--all", lsRemoteEntry.Sha1}, mrc...)...)

		if err != nil {
			return nil, errors.New("Unable to find common commit with " + lsRemoteEntry.Ref)
		}

		if common == lsRemoteEntry.Sha1 {
			context.Logger.Println("Already up-to-date with " + lsRemoteEntry.Ref)
			continue
		}

		if len(mrc) == 1 && common == mrc[0] && !nonFfMerge {
			context.Logger.Println("Fast-forwarding to: " + lsRemoteEntry.Ref)

			_, err := context.Repo.Git("read-tree", "-u", "-m", head, lsRemoteEntry.Sha1)

			if err != nil {
				return nil, err
			}

			mrc[0] = lsRemoteEntry.Sha1
			mrt, _ = context.Repo.Git("write-tree")
			continue
		}

		nonFfMerge = true

		context.Logger.Println("Trying simple merge with " + lsRemoteEntry.Ref)

		_, err = context.Repo.Git("read-tree", "-u", "-m", "--aggressive", common, mrt, lsRemoteEntry.Sha1)

		if err != nil {
			return nil, err
		}

		next, err := context.Repo.Git("write-tree")

		if err != nil {
			context.Logger.Println("Simple merge did not work, trying automatic merge.")
			_, err = context.Repo.Git("merge-index", "-o", "git-merge-one-file", "-a")

			if err != nil {
				context.Logger.Println("Automated merge did not work.")
				context.Logger.Println("Should not be doing an Octopus.")
				return nil, errors.New("")
			}

			next, _ = context.Repo.Git("write-tree")
		}

		mrc = append(mrc, lsRemoteEntry.Sha1)
		mrt = next
	}

	return mrc, nil
}

func octopusCommitMessage(remotes []git.LsRemoteEntry) string {
	buf := bytes.NewBufferString("Merged branches:\n")
	for _, lsRemoteEntry := range remotes {
		buf.WriteString(lsRemoteEntry.Ref + "\n")
	}
	buf.WriteString("\nCommit created by git-octopus " + VERSION + ".\n")
	return buf.String()
}
