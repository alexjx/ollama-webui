package agent

import "time"

// thinkingTimer counts only streamed reasoning phases, excluding prompt loading,
// answer generation, tools, and the pauses between model turns.
type thinkingTimer struct {
	observed bool
	active   bool
	started  time.Time
	duration time.Duration
}

func (timer *thinkingTimer) start(now time.Time) bool {
	if timer.active {
		return false
	}
	timer.observed = true
	timer.active = true
	timer.started = now
	return true
}

func (timer *thinkingTimer) stop(now time.Time) bool {
	if !timer.active {
		return false
	}
	timer.duration += now.Sub(timer.started)
	timer.active = false
	return true
}

func (timer *thinkingTimer) metadata() map[string]any {
	return map[string]any{
		"thinking_duration_ms": timer.duration.Milliseconds(),
		"thinking_active":      timer.active,
	}
}
