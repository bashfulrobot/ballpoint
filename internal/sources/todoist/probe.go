package todoist

import (
	"context"
	"log"
	"net/url"
	"sort"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// probeDeadline bounds one whole Probe call. The http.Client timeout is
// per-request, so without this a server that answers each request slowly, or
// keeps handing back pages under the cap, could still pin the process. A
// generous ceiling well above any real scope's wall clock.
const probeDeadline = 5 * time.Minute

// linkKey is the watermark key for a Todoist task.
func linkKey(taskID string) string { return "todoist:" + taskID }

// Probe fetches the whole scope: the task list, the project and section name
// maps, and every task's comments under a bounded concurrency group. It
// normalises the result and computes the delta against since.
//
// The three list fetches are fatal on failure; they gate the run. A single
// task's comment fetch is not. Comments are enrichment, so a transient failure
// on one caches that task without its comments and logs a warning rather than
// discarding the whole prefetch.
func (c *Client) Probe(ctx context.Context, since sources.Watermark) (sources.Delta, error) {
	ctx, cancel := context.WithTimeout(ctx, probeDeadline)
	defer cancel()

	var rawTasks []rawTask
	if err := c.getAll(ctx, "/tasks", nil, &rawTasks); err != nil {
		return sources.Delta{}, err
	}

	var rawProjects []rawNamed
	if err := c.getAll(ctx, "/projects", nil, &rawProjects); err != nil {
		return sources.Delta{}, err
	}
	var rawSections []rawNamed
	if err := c.getAll(ctx, "/sections", nil, &rawSections); err != nil {
		return sources.Delta{}, err
	}

	projects := nameMap(rawProjects)
	sections := nameMap(rawSections)

	tasks := make([]sources.Task, len(rawTasks))
	commentErrs := make([]error, len(rawTasks))
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(c.limit)

	for i := range rawTasks {
		i := i
		group.Go(func() error {
			task, err := rawTasks[i].toTask(projects, sections)
			if err != nil {
				// A timestamp that will not parse is a systemic format drift,
				// worth failing the whole run over.
				return err
			}

			q := url.Values{}
			q.Set("task_id", task.ID)
			var rawComments []rawComment
			if err := c.getAll(gctx, "/comments", q, &rawComments); err != nil {
				// A cancelled or timed-out context is a real abort; propagate
				// it. Any other comment failure is transient enrichment loss,
				// so keep the task without its comments.
				if ctx.Err() != nil {
					return ctx.Err()
				}
				commentErrs[i] = err
				tasks[i] = task
				return nil
			}

			comments := make([]sources.Comment, len(rawComments))
			for j, rc := range rawComments {
				comments[j] = rc.toComment()
			}
			task.Comments = comments

			tasks[i] = task
			return nil
		})
	}

	if err := group.Wait(); err != nil {
		return sources.Delta{}, err
	}

	if failed, first := countErrs(commentErrs); failed > 0 {
		log.Printf("todoist probe: %d of %d comment fetches failed; those tasks are cached without comments (first error: %v)",
			failed, len(rawTasks), first)
	}

	next := sources.Watermark{}
	var changed []string
	for _, task := range tasks {
		key := linkKey(task.ID)
		next[key] = task.UpdatedAt

		prev, ok := since[key]
		if !ok || task.UpdatedAt.After(prev) {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)

	return sources.Delta{Tasks: tasks, Changed: changed, Next: next}, nil
}

// countErrs returns how many entries are non-nil and the first non-nil error,
// for the degraded-fetch warning.
func countErrs(errs []error) (int, error) {
	n := 0
	var first error
	for _, err := range errs {
		if err != nil {
			n++
			if first == nil {
				first = err
			}
		}
	}
	return n, first
}

// nameMap turns a list of id/name pairs into an id to name lookup.
func nameMap(items []rawNamed) map[string]string {
	m := make(map[string]string, len(items))
	for _, it := range items {
		m[it.ID] = it.Name
	}
	return m
}
