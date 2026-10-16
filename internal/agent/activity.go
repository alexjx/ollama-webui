package agent

import "sync"

// Activity records ordering without duplicating persisted text and tool output.
// Text offsets are UTF-8 byte offsets into RunResult's aggregate strings.
type Activity struct {
	Version int             `json:"version"`
	Blocks  []ActivityBlock `json:"blocks"`
}
type ActivityBlock struct {
	Type       string `json:"type"`
	Turn       int    `json:"turn"`
	Start      *int   `json:"start,omitempty"`
	End        *int   `json:"end,omitempty"`
	StepID     int64  `json:"step_id,omitempty"`
	RunID      int64  `json:"run_id,omitempty"`
	DurationMS *int64 `json:"duration_ms,omitempty"`
}
type activityRecorder struct {
	mu                      sync.Mutex
	blocks                  []ActivityBlock
	turn, content, thinking int
	thinkingDuration        int64
	steps, runs             map[int64]bool
}

func (r *activityRecorder) record(event Event) {
	if event.Turn > 0 {
		r.turn = event.Turn
	}
	switch event.Type {
	case "thinking.delta", "delta":
		if event.Content == "" {
			return
		}
		kind, offset := "content", &r.content
		if event.Type == "thinking.delta" {
			kind, offset = "thinking", &r.thinking
		}
		start, end := *offset, *offset+len(event.Content)
		*offset = end
		n := len(r.blocks)
		if n > 0 && r.blocks[n-1].Type == kind && r.blocks[n-1].Turn == r.turn {
			r.blocks[n-1].End = &end
		} else {
			block := ActivityBlock{Type: kind, Turn: r.turn, Start: &start, End: &end}
			if kind == "thinking" {
				zero := int64(0)
				block.DurationMS = &zero
			}
			r.blocks = append(r.blocks, block)
		}
	case "thinking.timing":
		duration, _ := event.Metadata["thinking_duration_ms"].(int64)
		delta := duration - r.thinkingDuration
		r.thinkingDuration = duration
		if delta > 0 {
			for i := len(r.blocks) - 1; i >= 0; i-- {
				if r.blocks[i].Type == "thinking" {
					*r.blocks[i].DurationMS += delta
					break
				}
			}
		}
	default:
		if event.Step != nil {
			if r.steps == nil {
				r.steps = map[int64]bool{}
			}
			if !r.steps[event.Step.ID] {
				r.steps[event.Step.ID] = true
				r.blocks = append(r.blocks, ActivityBlock{Type: "tool", Turn: r.turn, StepID: event.Step.ID})
			}
		}
		if event.Run != nil {
			if r.runs == nil {
				r.runs = map[int64]bool{}
			}
			if !r.runs[event.Run.ID] {
				r.runs[event.Run.ID] = true
				r.blocks = append(r.blocks, ActivityBlock{Type: "subagent", Turn: r.turn, RunID: event.Run.ID})
			}
		}
	}
}
func (r *activityRecorder) wrap(emit func(Event) error) func(Event) error {
	return func(event Event) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.record(event)
		if event.Turn == 0 && event.Run != nil {
			event.Turn = r.turn
		}
		return emit(event)
	}
}
func (r *activityRecorder) snapshot() Activity {
	r.mu.Lock()
	defer r.mu.Unlock()
	blocks := make([]ActivityBlock, len(r.blocks))
	for i, block := range r.blocks {
		blocks[i] = block
		if block.Start != nil {
			value := *block.Start
			blocks[i].Start = &value
		}
		if block.End != nil {
			value := *block.End
			blocks[i].End = &value
		}
		if block.DurationMS != nil {
			value := *block.DurationMS
			blocks[i].DurationMS = &value
		}
	}
	return Activity{Version: 1, Blocks: blocks}
}
