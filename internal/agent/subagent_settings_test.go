package agent

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"ollama-webui/internal/ollama"
)

func TestSubagentKeepsInheritedModelDefaultsUnset(t *testing.T) {
	ctx := context.Background()
	database, conversationID := artifactTestStore(t, ctx)
	assistant, err := database.AddMessage(ctx, conversationID, "assistant", "", "streaming")
	if err != nil {
		t.Fatal(err)
	}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{{{Message: ollama.Message{Content: "done"}, Done: true}}}}
	orchestrator, err := NewSubagentOrchestrator(database, chat, &recordingShell{}, nil, t.TempDir(), 2048, 2, 1024, 1024)
	if err != nil {
		t.Fatal(err)
	}
	_, err = orchestrator.Execute(ctx, SubagentRequest{ConversationID: conversationID, RequestingMessageID: assistant.ID, Model: "parent-model", Task: "focused"}, func(Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	request := chat.requests[0]
	if request.Model != "parent-model" || request.Options != nil || request.Think != nil {
		t.Fatalf("child invented overrides for inherited model defaults: %#v", request)
	}
	for _, tool := range request.Tools {
		if tool.Function.Name == "delegate_task" || tool.Function.Name == "label_image_directory" {
			t.Fatal("child can recursively delegate")
		}
	}
}

type serializedChildChat struct {
	started    chan struct{}
	release    chan struct{}
	active     atomic.Int32
	overlapped atomic.Bool
}

func (chat *serializedChildChat) Chat(ctx context.Context, _ ollama.ChatRequest, emit func(ollama.ChatChunk) error) error {
	if chat.active.Add(1) > 1 {
		chat.overlapped.Store(true)
	}
	defer chat.active.Add(-1)
	chat.started <- struct{}{}
	select {
	case <-chat.release:
		return emit(ollama.ChatChunk{Message: ollama.Message{Content: "done"}, Done: true})
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestSubagentSerializesChildrenAndCancelsQueuedWork(t *testing.T) {
	for _, cancelQueued := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel_queued=%v", cancelQueued), func(t *testing.T) {
			ctx := context.Background()
			database, conversationID := artifactTestStore(t, ctx)
			assistant, err := database.AddMessage(ctx, conversationID, "assistant", "", "streaming")
			if err != nil {
				t.Fatal(err)
			}
			chat := &serializedChildChat{started: make(chan struct{}, 2), release: make(chan struct{}, 2)}
			orchestrator, err := NewSubagentOrchestrator(database, chat, &recordingShell{}, nil, t.TempDir(), 2048, 2, 1024, 1024)
			if err != nil {
				t.Fatal(err)
			}
			parentCtx, cancelParent := context.WithCancel(ctx)
			defer cancelParent()
			request := SubagentRequest{ConversationID: conversationID, RequestingMessageID: assistant.ID, Model: "parent", Task: "focused"}
			firstDone := make(chan error, 1)
			go func() {
				_, err := orchestrator.Execute(parentCtx, request, func(Event) error { return nil })
				firstDone <- err
			}()
			select {
			case <-chat.started:
			case <-time.After(time.Second):
				t.Fatal("first child did not start")
			}
			secondCtx, cancelSecond := context.WithCancel(parentCtx)
			defer cancelSecond()
			queued := make(chan struct{}, 1)
			cancelled := make(chan struct{}, 1)
			secondDone := make(chan error, 1)
			go func() {
				_, err := orchestrator.Execute(secondCtx, request, func(event Event) error {
					if event.Type == "subagent.queued" {
						queued <- struct{}{}
					}
					if event.Type == "subagent.cancelled" {
						cancelled <- struct{}{}
					}
					return nil
				})
				secondDone <- err
			}()
			select {
			case <-queued:
			case <-time.After(time.Second):
				t.Fatal("second child was not queued")
			}
			select {
			case <-chat.started:
				t.Fatal("second child ran concurrently")
			case <-time.After(30 * time.Millisecond):
			}
			if cancelQueued {
				cancelSecond()
				if err := <-secondDone; !errors.Is(err, context.Canceled) {
					t.Fatalf("queued cancellation failed: %v", err)
				}
				select {
				case <-cancelled:
				default:
					t.Fatal("queued child did not emit cancellation")
				}
			}
			chat.release <- struct{}{}
			if err := <-firstDone; err != nil {
				t.Fatal(err)
			}
			if !cancelQueued {
				select {
				case <-chat.started:
				case <-time.After(time.Second):
					t.Fatal("queued child did not start after first finished")
				}
				chat.release <- struct{}{}
				if err := <-secondDone; err != nil {
					t.Fatal(err)
				}
			}
			if chat.overlapped.Load() {
				t.Fatal("child model requests overlapped")
			}
		})
	}
}
