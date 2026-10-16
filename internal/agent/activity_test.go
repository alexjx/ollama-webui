package agent

import (
	"context"
	"encoding/json"
	"errors"
	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
	"reflect"
	"testing"
)

func TestActivityRecordsChronologyAndUTF8Ranges(t *testing.T) {
	call := ollama.ToolCall{Function: ollama.ToolCallFunction{Name: "shell", Arguments: json.RawMessage(`{"command":"pwd"}`)}}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{
		{{Message: ollama.Message{Thinking: "想🙂"}}, {Message: ollama.Message{Thinking: "更多", Content: "Checking"}}, {Message: ollama.Message{Thinking: "again"}}, {Message: ollama.Message{ToolCalls: []ollama.ToolCall{call}}, Done: true}},
		{{Message: ollama.Message{Thinking: "第二轮", Content: "Done"}}, {Done: true}},
	}}
	result, err := (Runner{Chat: chat, Steps: &memorySteps{}, Shell: &recordingShell{}, MaxTurns: 3}).Run(context.Background(), RunInput{Mode: "agent"}, func(Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	activity := result.Metadata["activity"].(Activity)
	var kinds, texts []string
	var turns []int
	for _, block := range activity.Blocks {
		kinds = append(kinds, block.Type)
		turns = append(turns, block.Turn)
		if block.Start != nil {
			text := result.Content
			if block.Type == "thinking" {
				text = result.Thinking
			}
			texts = append(texts, text[*block.Start:*block.End])
		}
	}
	if !reflect.DeepEqual(kinds, []string{"thinking", "content", "thinking", "tool", "thinking", "content"}) || !reflect.DeepEqual(turns, []int{1, 1, 1, 1, 2, 2}) {
		t.Fatalf("bad order: %#v", activity)
	}
	if !reflect.DeepEqual(texts, []string{"想🙂更多", "Checking", "again", "\n\n第二轮", "\n\nDone"}) {
		t.Fatalf("bad ranges: %#v", texts)
	}
	if activity.Blocks[3].StepID != 1 {
		t.Fatal("missing tool reference")
	}
}

func TestActivityTimingReentryReferencesAndSnapshot(t *testing.T) {
	var r activityRecorder
	emit := r.wrap(func(Event) error { return nil })
	for _, event := range []Event{
		{Type: "turn.started", Turn: 1},
		{Type: "thinking.delta", Content: "a"},
		{Type: "thinking.timing", Metadata: map[string]any{"thinking_duration_ms": int64(20)}},
		{Type: "delta", Content: "b"},
		{Type: "thinking.delta", Content: "c"},
		{Type: "thinking.timing", Metadata: map[string]any{"thinking_duration_ms": int64(35)}},
		{Type: "tool.started", Step: &store.AgentStep{ID: 4}},
		{Type: "subagent.started", Run: &store.AgentRun{ID: 8}},
		{Type: "subagent.done", Run: &store.AgentRun{ID: 8}},
		{Type: "tool.done", Step: &store.AgentStep{ID: 4}},
	} {
		if err := emit(event); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := r.snapshot()
	if len(snapshot.Blocks) != 5 || *snapshot.Blocks[0].DurationMS != 20 || *snapshot.Blocks[2].DurationMS != 15 || snapshot.Blocks[4].Turn != 1 {
		t.Fatalf("bad snapshot: %#v", snapshot)
	}
	*snapshot.Blocks[0].DurationMS = 99
	*snapshot.Blocks[0].Start = 99
	if *r.snapshot().Blocks[0].DurationMS != 20 || *r.snapshot().Blocks[0].Start != 0 {
		t.Fatal("snapshot shares mutable state")
	}
}

func TestActivityRetainedOnDeliveryErrorAndCancellation(t *testing.T) {
	for _, failAt := range []string{"activity.started", "thinking.delta", "delta", "turn.done"} {
		t.Run(failAt, func(t *testing.T) {
			chat := &scriptedChat{turns: [][]ollama.ChatChunk{{{Message: ollama.Message{Thinking: "思🙂", Content: "reply"}}, {Done: true}}}}
			result, err := (Runner{Chat: chat}).Run(context.Background(), RunInput{Mode: "chat"}, func(e Event) error {
				if e.Type == failAt {
					return context.Canceled
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v", err)
			}
			activity, ok := result.Metadata["activity"].(Activity)
			if !ok || activity.Version != 1 || activity.Blocks == nil {
				t.Fatal("missing activity")
			}
			for _, b := range activity.Blocks {
				text := result.Content
				if b.Type == "thinking" {
					text = result.Thinking
				}
				if b.End != nil && *b.End > len(text) {
					t.Fatal("range exceeds persisted text")
				}
			}
			if failAt == "thinking.delta" && (len(activity.Blocks) != 1 || result.Thinking != "思🙂") {
				t.Fatal("failed emission lost accumulated text")
			}
		})
	}
}
