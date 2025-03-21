package graphqlbackend

import (
	"context"
	"strconv"
	"sync"

	"github.com/sourcegraph/sourcegraph/cmd/frontend/graphqlbackend/graphqlutil"
	"github.com/sourcegraph/sourcegraph/internal/database"
	"github.com/sourcegraph/sourcegraph/internal/gitserver"
	"github.com/sourcegraph/sourcegraph/internal/gitserver/gitdomain"
	"github.com/sourcegraph/sourcegraph/lib/errors"
)

type gitCommitConnectionResolver struct {
	db              database.DB
	gitserverClient gitserver.Client
	revisionRange   string

	first  *int32
	query  *string
	path   *string
	follow bool
	author *string

	// after corresponds to --after in the git log / git rev-spec commands. Not to be confused with
	// "after" when used as an offset for pagination. For pagination we use "offset" as the name of
	// the field. See next field.
	after       *string
	afterCursor *string
	before      *string

	repo *RepositoryResolver

	// cache results because it is used by multiple fields
	once    sync.Once
	commits []*gitdomain.Commit
	err     error
}

func toValue[T any](v *T) any {
	var result T
	if v != nil {
		return *v
	}

	return result
}

// afterCursorAsInt will parse the afterCursor field and return it as an int. If no value is set, it
// will return 0. It returns a non-nil error if there are any errors in parsing the input string.
func (r *gitCommitConnectionResolver) afterCursorAsInt() (int, error) {
	v := toValue(r.afterCursor).(string)
	if v == "" {
		return 0, nil
	}

	return strconv.Atoi(v)
}

func (r *gitCommitConnectionResolver) compute(ctx context.Context) ([]*gitdomain.Commit, error) {
	do := func() ([]*gitdomain.Commit, error) {
		var n int32
		// IMPORTANT: We cannot use toValue here because we toValue will return 0 if r.first is nil.
		// And n will be incorrectly set to 1. A nil value for r.first implies no limits, so skip
		// setting a value for n completely.
		if r.first != nil {
			n = *r.first
			n++ // fetch +1 additional result so we can determine if a next page exists
		}

		// If no value for afterCursor is set, then skip is 0. And this is fine as --skip=0 is the
		// same as not setting the flag.
		afterCursor, err := r.afterCursorAsInt()
		if err != nil {
			return []*gitdomain.Commit{}, errors.Wrap(err, "failed to parse afterCursor")
		}

		return r.gitserverClient.Commits(ctx, r.repo.RepoName(), gitserver.CommitsOptions{
			Range:        r.revisionRange,
			N:            uint(n),
			MessageQuery: toValue(r.query).(string),
			Author:       toValue(r.author).(string),
			After:        toValue(r.after).(string),
			Skip:         uint(afterCursor),
			Before:       toValue(r.before).(string),
			Path:         toValue(r.path).(string),
			Follow:       r.follow,
		})
	}

	r.once.Do(func() { r.commits, r.err = do() })
	return r.commits, r.err
}

func (r *gitCommitConnectionResolver) Nodes(ctx context.Context) ([]*GitCommitResolver, error) {
	commits, err := r.compute(ctx)
	if err != nil {
		return nil, err
	}

	if r.first != nil && len(commits) > int(*r.first) {
		// Don't return +1 results, which is used to determine if next page exists.
		commits = commits[:*r.first]
	}

	resolvers := make([]*GitCommitResolver, len(commits))
	for i, commit := range commits {
		resolvers[i] = NewGitCommitResolver(r.db, r.gitserverClient, r.repo, commit.ID, commit)
	}

	return resolvers, nil
}

func (r *gitCommitConnectionResolver) TotalCount(ctx context.Context) (*int32, error) {
	if r.first != nil {
		// Return indeterminate total count if the caller requested an incomplete list of commits
		// (which means we'd need an extra and expensive Git operation to determine the total
		// count). This is to avoid `totalCount` taking significantly longer than `nodes` to
		// compute, which would be unexpected to many API clients.
		return nil, nil
	}
	commits, err := r.compute(ctx)
	if err != nil {
		return nil, err
	}
	n := int32(len(commits))
	return &n, nil
}

func (r *gitCommitConnectionResolver) PageInfo(ctx context.Context) (*graphqlutil.PageInfo, error) {
	commits, err := r.compute(ctx)
	if err != nil {
		return nil, err
	}

	totalCommits := len(commits)
	// If no limit is set, we have retrieved all the commits and there is no next page.
	if r.first == nil {
		return graphqlutil.HasNextPage(false), nil
	}

	limit := int(*r.first)

	// If a limit is set, we attempt to fetch N+1 commits to know if there is a next page or not. If
	// we have more than N commits then we have a next page.
	if totalCommits > limit {
		// Pagination logic below.
		//
		// Example:
		// Request 1: first: 100
		// Response 1: commits: 1 to 100, endCursor: 100
		//
		// Request 2: first: 100, afterCursor: 100 (endCursor from previous request)
		// Response 2: commits: 101 to 200, endCursor: 200 (first + offset)
		//
		// Request 3: first: 50, afterCursor: 200 (endCursor from previous request)
		// Response 3: commits: 201 to 250, endCursor: 250 (first + offset)
		after, err := r.afterCursorAsInt()
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse afterCursor")
		}

		endCursor := limit + after
		return graphqlutil.NextPageCursor(strconv.Itoa(endCursor)), nil
	}

	return graphqlutil.HasNextPage(false), nil
}
