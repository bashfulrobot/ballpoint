package todoist

import (
	"context"
	"net/url"
	"sort"

	"golang.org/x/sync/errgroup"

	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// linkKey is the watermark key for a Todoist task.
func linkKey(taskID string) string { return "todoist:" + taskID }

// Probe fetches the whole scope: the task list, the project and section name
// maps, and every task's comments under a bounded concurrency group. It
// normalises the result and computes the delta against since.
func (c *Client) Probe(ctx context.Context, since sources.Watermark) (sources.Delta, error) {
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
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(c.limit)

	for i := range rawTasks {
		i := i
		group.Go(func() error {
			task := rawTasks[i].toTask(projects, sections)

			q := url.Values{}
			q.Set("task_id", task.ID)
			var rawComments []rawComment
			if err := c.getAll(gctx, "/comments", q, &rawComments); err != nil {
				return err
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

// nameMap turns a list of id/name pairs into an id to name lookup.
func nameMap(items []rawNamed) map[string]string {
	m := make(map[string]string, len(items))
	for _, it := range items {
		m[it.ID] = it.Name
	}
	return m
}
