package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"ollama-webui/internal/ollama"
)

func TestThinkingTimerAccumulatesOnlyActivePhases(t *testing.T) {
	var timer thinkingTimer
	start := time.Unix(0, 0)
	if timer.stop(start) || timer.observed {
		t.Fatal("an unused timer reported thinking")
	}
	if !timer.start(start.Add(time.Hour)) || timer.start(start.Add(time.Hour+time.Second)) {
		t.Fatal("a repeated thinking chunk restarted the timer")
	}
	if !timer.stop(start.Add(time.Hour+1500*time.Millisecond)) || timer.stop(start.Add(2*time.Hour)) {
		t.Fatal("stopping an inactive timer changed its duration")
	}
	timer.start(start.Add(3 * time.Hour))
	timer.stop(start.Add(3*time.Hour + 750*time.Millisecond))
	if timer.duration.Milliseconds() != 2250 || timer.active || !timer.observed {
		t.Fatalf("timer counted inactive gaps: %#v", timer)
	}
}

type reasoningChatFunc func(context.Context, ollama.ChatRequest, func(ollama.ChatChunk) error) error

func (chat reasoningChatFunc) Chat(ctx context.Context, request ollama.ChatRequest, onChunk func(ollama.ChatChunk) error) error {
	return chat(ctx, request, onChunk)
}

type reasoningShell struct{}

func (reasoningShell) Run(context.Context, string, time.Duration) ShellResult {
	// synctest advances its fake clock, without spending real time.
	time.Sleep(time.Hour)
	return ShellResult{Output: "ready"}
}

func TestRunnerThinkingTimingExcludesToolsLoadingAndAnswerGeneration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		turn := 0
		chat := reasoningChatFunc(func(_ context.Context, _ ollama.ChatRequest, chunk func(ollama.ChatChunk) error) error {
			turn++
			time.Sleep(2 * time.Hour) // model load, prompt evaluation and inter-turn delay
			if err := chunk(ollama.ChatChunk{Message: ollama.Message{Thinking: "reasoning"}}); err != nil {
				return err
			}
			time.Sleep(100 * time.Millisecond)
			if err := chunk(ollama.ChatChunk{Message: ollama.Message{Thinking: " continued"}}); err != nil {
				return err
			}
			time.Sleep(200 * time.Millisecond)
			if turn == 1 {
				call := ollama.ToolCall{Function: ollama.ToolCallFunction{Name: "shell", Arguments: json.RawMessage(`{"command":"pwd"}`)}}
				return chunk(ollama.ChatChunk{Message: ollama.Message{ToolCalls: []ollama.ToolCall{call}}, Done: true})
			}
			if err := chunk(ollama.ChatChunk{Message: ollama.Message{Content: "answer"}}); err != nil {
				return err
			}
			time.Sleep(time.Hour) // answer generation is not thinking
			// A later reasoning phase in the same model call must start a new timer.
			if err := chunk(ollama.ChatChunk{Message: ollama.Message{Thinking: "extra reasoning"}}); err != nil {
				return err
			}
			time.Sleep(50 * time.Millisecond)
			return chunk(ollama.ChatChunk{Done: true})
		})
		var timingEvents []map[string]any
		result, err := (Runner{Chat: chat, Steps: &memorySteps{}, Shell: reasoningShell{}, MaxTurns: 2}).Run(
			context.Background(), RunInput{Model: "thinker"}, func(event Event) error {
				if event.Type == "thinking.timing" {
					timingEvents = append(timingEvents, event.Metadata)
				}
				return nil
			})
		if err != nil || result.Metadata["thinking_duration_ms"] != int64(650) {
			t.Fatalf("incorrect reasoning duration: %#v, %v", result.Metadata, err)
		}
		want := []map[string]any{
			{"thinking_duration_ms": int64(0), "thinking_active": true},
			{"thinking_duration_ms": int64(300), "thinking_active": false},
			{"thinking_duration_ms": int64(300), "thinking_active": true},
			{"thinking_duration_ms": int64(600), "thinking_active": false},
			{"thinking_duration_ms": int64(600), "thinking_active": true},
			{"thinking_duration_ms": int64(650), "thinking_active": false},
		}
		if !reflect.DeepEqual(timingEvents, want) {
			t.Fatalf("timing events = %#v; want %#v", timingEvents, want)
		}
	})
}

func TestRunnerRetainsThinkingTimingAndEarlierMetricsOnInterruption(t *testing.T) {
	for _, interruption := range []error{context.Canceled, errors.New("stream failed")} {
		t.Run(interruption.Error(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				turn := 0
				chat := reasoningChatFunc(func(_ context.Context, _ ollama.ChatRequest, chunk func(ollama.ChatChunk) error) error {
					turn++
					if turn == 1 {
						call := ollama.ToolCall{Function: ollama.ToolCallFunction{Name: "shell", Arguments: json.RawMessage(`{"command":"pwd"}`)}}
						return chunk(ollama.ChatChunk{Message: ollama.Message{ToolCalls: []ollama.ToolCall{call}}, Done: true, PromptEvalCount: 42})
					}
					if err := chunk(ollama.ChatChunk{Message: ollama.Message{Thinking: "partial reasoning"}}); err != nil {
						return err
					}
					time.Sleep(250 * time.Millisecond)
					return interruption
				})
				var stopped Event
				result, err := (Runner{Chat: chat, Steps: &memorySteps{}, Shell: &recordingShell{}, MaxTurns: 2}).Run(
					context.Background(), RunInput{Model: "thinker"}, func(event Event) error {
						if event.Type == "thinking.timing" && event.Metadata["thinking_active"] == false {
							stopped = event
						}
						return nil
					})
				if !errors.Is(err, interruption) || result.Thinking != "partial reasoning" || result.Metadata["thinking_duration_ms"] != int64(250) || result.Metadata["prompt_eval_count"] != 42 || result.Metadata["agent_turns"] != 1 {
					t.Fatalf("interruption lost timing or prior metrics: %#v, %v", result, err)
				}
				if stopped.Turn != 2 || stopped.Metadata["thinking_duration_ms"] != int64(250) {
					t.Fatalf("interrupted timer did not stop: %#v", stopped)
				}
			})
		})
	}
}

func TestRunnerZeroDurationThinkingAndNonThinkingAreDistinct(t *testing.T) {
	for _, thought := range []string{"", "instant reasoning"} {
		chat := &scriptedChat{turns: [][]ollama.ChatChunk{{{Message: ollama.Message{Thinking: thought, Content: "answer"}, Done: true}}}}
		result, err := (Runner{Chat: chat}).Run(context.Background(), RunInput{Mode: "chat"}, func(Event) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		_, measured := result.Metadata["thinking_duration_ms"]
		if measured != (thought != "") {
			t.Fatalf("thinking presence %q disagrees with timing: %#v", thought, result.Metadata)
		}
	}
}
