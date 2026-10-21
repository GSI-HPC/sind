// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import "github.com/GSI-HPC/sind/internal/ui/store"

func init() { defaultRegistry.AddFragment(tasksFragment()) }

// tasksFragment is the task dock at the bottom right of every page: the
// commands that sind ui started, with their progress.
func tasksFragment() Fragment {
	return Fragment{
		Name:   "tasks",
		Topics: []store.Topic{store.TopicTasks},
		Shell:  true,
	}
}
