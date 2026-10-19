// relationships:
//
//	references: [runner, ../../lifecycle/lifecycle]
package runner

import "github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"

// StopReason identifies the successful terminal boundary, independent of whether
// commands were configured or skipped by markers. Values match the upstream CLI.
type StopReason string

const (
	SkipNonBlocking        StopReason = "skipNonBlocking"
	Prebuild               StopReason = "prebuild"
	StopForPersonalization StopReason = "stopForPersonalization"
	Done                   StopReason = "done"
)

// LifecycleControls select upstream lifecycle stop points. Later commands are
// omitted, not started in the background. WaitFor comes from merged metadata.
type LifecycleControls struct {
	SkipNonBlocking        bool
	Prebuild               bool
	StopForPersonalization bool
	WaitFor                string
}

// selectPlan applies boundaries even when the boundary hook has no commands.
// Dotfiles run after postCreate and before the personalization stop point.
func (c LifecycleControls) selectPlan(plan []lifecycle.Phase) ([]lifecycle.Phase, bool, StopReason) {
	limit := 5
	reason := Done
	if c.SkipNonBlocking {
		waitFor := c.WaitFor
		if waitFor == "" {
			waitFor = string(lifecycle.HookUpdateContent)
		}
		limit = hookOrder(lifecycle.Hook(waitFor))
		if limit < 5 {
			reason = SkipNonBlocking
		}
	}
	if c.Prebuild && limit > 2 {
		limit = 2
		reason = Prebuild
	}
	if c.StopForPersonalization && limit > 3 {
		limit = 3
		reason = StopForPersonalization
	}
	selected := make([]lifecycle.Phase, 0, len(plan))
	for _, p := range plan {
		if hookOrder(p.Hook) <= limit {
			selected = append(selected, p)
		}
	}
	// A waitFor postCreate stop precedes dotfiles; personalization follows it.
	dotfiles := limit >= 3 && !(c.SkipNonBlocking && c.WaitFor == string(lifecycle.HookPostCreate))
	return selected, dotfiles, reason
}

func hookOrder(h lifecycle.Hook) int {
	switch h {
	case "initializeCommand":
		return 0
	case lifecycle.HookOnCreate:
		return 1
	case lifecycle.HookUpdateContent:
		return 2
	case lifecycle.HookPostCreate:
		return 3
	case lifecycle.HookPostStart:
		return 4
	default:
		return 5
	}
}
