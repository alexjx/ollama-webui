package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"ollama-webui/internal/agent"
	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

type titleTestPayload struct {
	Model    string              `json:"model"`
	Messages []ollama.Message    `json:"messages"`
	Tools    json.RawMessage     `json:"tools"`
	Think    json.RawMessage     `json:"think"`
	Options  *ollama.ChatOptions `json:"options"`
}

type titleTestFixture struct {
	api      *Server
	database *store.Store
	path     string
	titles   chan titleTestPayload
}

func newTitleTestFixture(t *testing.T, respond func(http.ResponseWriter, *http.Request, bool)) titleTestFixture {
	t.Helper()
	titles := make(chan titleTestPayload, 16)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			io.WriteString(w, `{"models":[{"name":"same-model","capabilities":["completion","tools","thinking"]}]}`)
			return
		}
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		var payload titleTestPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		isTitle := len(payload.Messages) > 0 && payload.Messages[0].Content == titleSystemPrompt
		if isTitle {
			titles <- payload
		}
		respond(w, r, isTitle)
	}))
	path := filepath.Join(t.TempDir(), "titles.db")
	database, err := store.Open(context.Background(), path)
	if err != nil {
		upstream.Close()
		t.Fatal(err)
	}
	client := ollama.NewClient(upstream.URL)
	api := New(database, client, agent.Runner{
		Chat: client, Steps: database, MaxTurns: 3,
		Shell: agent.ShellExecutor{Workspace: t.TempDir(), Timeout: time.Second},
	}, RuntimeSettings{ContextTokens: 4096}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		api.Close()
		upstream.Close()
		database.Close()
	})
	return titleTestFixture{api: api, database: database, path: path, titles: titles}
}

func titleTestReply(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	json.NewEncoder(w).Encode(map[string]any{
		"message": map[string]string{"role": "assistant", "content": content},
		"done":    true, "prompt_eval_count": 21, "eval_count": 7,
	})
}

func createTitleTestConversation(t *testing.T, fixture titleTestFixture, mode string) store.Conversation {
	t.Helper()
	response := httptest.NewRecorder()
	fixture.api.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/conversations", strings.NewReader(fmt.Sprintf(
		`{"title":"Initial title","model":"same-model","mode":%q,"thinking_mode":"high","system_prompt":"An unrelated custom system prompt."}`, mode))))
	if response.Code != http.StatusCreated {
		t.Fatalf("create conversation: %d %s", response.Code, response.Body.String())
	}
	var conversation store.Conversation
	if err := json.Unmarshal(response.Body.Bytes(), &conversation); err != nil || !conversation.AutoTitlePending {
		t.Fatalf("new API conversation not eligible for automatic naming: %#v, %v", conversation, err)
	}
	return conversation
}

func titleTestMessage(t *testing.T, fixture titleTestFixture, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	fixture.api.ServeHTTP(response, httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/conversations/%d/messages", id), strings.NewReader(body)))
	return response
}

func waitForTitleRequest(t *testing.T, fixture titleTestFixture) titleTestPayload {
	t.Helper()
	select {
	case payload := <-fixture.titles:
		return payload
	case <-time.After(3 * time.Second):
		t.Fatal("automatic title request did not start")
		return titleTestPayload{}
	}
}

func waitForTitleFinished(t *testing.T, fixture titleTestFixture, id int64) store.Conversation {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conversation, err := fixture.database.GetConversation(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if !conversation.AutoTitlePending {
			return conversation
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("automatic title attempt did not finish")
	return store.Conversation{}
}

func TestFirstTurnAutomaticallyNamesChatAndAgentWithoutDelayingDone(t *testing.T) {
	for _, mode := range []string{"chat", "agent"} {
		t.Run(mode, func(t *testing.T) {
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			fixture := newTitleTestFixture(t, func(w http.ResponseWriter, r *http.Request, isTitle bool) {
				if isTitle {
					select {
					case <-release:
						titleTestReply(w, `Title: "Planning a mountain trip"`)
					case <-r.Context().Done():
					}
					return
				}
				titleTestReply(w, "Choose a route and check the weather.")
			})
			t.Cleanup(unblock)
			conversation := createTitleTestConversation(t, fixture, mode)
			response := titleTestMessage(t, fixture, conversation.ID, `{"content":"How do I plan a mountain trip?"}`)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"type":"done"`) || strings.Contains(response.Body.String(), `"type":"error"`) {
				t.Fatalf("first response did not finish independently: %d %s", response.Code, response.Body.String())
			}
			payload := waitForTitleRequest(t, fixture)
			if payload.Model != conversation.Model || len(payload.Messages) != 2 || len(payload.Tools) != 0 || string(payload.Think) != "false" {
				t.Fatalf("naming used different model, tools, thinking or transcript: %#v", payload)
			}
			var source map[string]string
			if err := json.Unmarshal([]byte(payload.Messages[1].Content), &source); err != nil || source["first_user_input"] != "How do I plan a mountain trip?" || source["first_assistant_reply"] != "Choose a route and check the weather." {
				t.Fatalf("naming omitted first question/reply: %#v, %v", source, err)
			}
			before, err := fixture.database.GetConversation(context.Background(), conversation.ID)
			if err != nil || before.Title != "Initial title" || !before.AutoTitlePending {
				t.Fatalf("held title changed session prematurely: %#v, %v", before, err)
			}
			original, err := fixture.database.Messages(context.Background(), conversation.ID)
			if err != nil || len(original) != 2 || original[1].Status != "complete" {
				t.Fatalf("completed first turn not persisted: %#v, %v", original, err)
			}
			unblock()
			updated := waitForTitleFinished(t, fixture, conversation.ID)
			if updated.Title != "Planning a mountain trip" {
				t.Fatalf("generated title not applied: %#v", updated)
			}
			reopened, err := store.Open(context.Background(), fixture.path)
			if err != nil {
				t.Fatal(err)
			}
			stored, readErr := reopened.GetConversation(context.Background(), conversation.ID)
			reopened.Close()
			if readErr != nil || stored.Title != updated.Title || stored.AutoTitlePending {
				t.Fatalf("generated title not durable: %#v, %v", stored, readErr)
			}
			after, err := fixture.database.Messages(context.Background(), conversation.ID)
			if err != nil {
				t.Fatal(err)
			}
			beforeJSON, _ := json.Marshal(original)
			afterJSON, _ := json.Marshal(after)
			if string(beforeJSON) != string(afterJSON) {
				t.Fatalf("naming changed transcript or performance metadata: before=%s after=%s", beforeJSON, afterJSON)
			}
			response = titleTestMessage(t, fixture, conversation.ID, `{"content":"What should I pack?"}`)
			if !strings.Contains(response.Body.String(), `"type":"done"`) {
				t.Fatalf("second turn failed: %s", response.Body.String())
			}
			fixture.api.titleJobs.Wait()
			select {
			case extra := <-fixture.titles:
				t.Fatalf("second turn requested another title: %#v", extra)
			default:
			}
		})
	}
}

func TestConversationTitlesDisableDefaultThinking(t *testing.T) {
	for _, test := range []struct{ name, prompt string }{{"automatic", titleSystemPrompt}, {"manual", latestTitleSystemPrompt}} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload titleTestPayload
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
					return
				}
				// Default reasoning consumes the output allowance before an answer.
				if string(payload.Think) != "false" {
					json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"role": "assistant", "thinking": strings.Repeat("reasoning ", 256)}, "done": true, "done_reason": "length", "eval_count": 256})
					return
				}
				if payload.Options != nil && payload.Options.NumPredict != nil {
					t.Error("title generation retains a token cap that can exhaust reasoning before the answer")
				}
				json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"role": "assistant", "thinking": "Never use reasoning as the title", "content": "Input box resizing"}, "done": true})
			}))
			defer upstream.Close()
			api := &Server{ollama: ollama.NewClient(upstream.URL)}
			title, err := api.requestConversationTitle(context.Background(), "reasoning-model", test.prompt, `{"user":"Resize the input","answer":"Drag the handle"}`)
			if err != nil || title != "Input box resizing" {
				t.Fatalf("reasoning model failed to produce a title: %q, %v", title, err)
			}
		})
	}
}

func TestConversationTitleAllowsUnavoidableReasoningBeforeAnswer(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload titleTestPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		message := map[string]string{"role": "assistant", "thinking": strings.Repeat("reasoning ", 512)}
		if payload.Options == nil || payload.Options.NumPredict == nil || *payload.Options.NumPredict > 512 {
			message["content"] = "Input box resizing"
		}
		json.NewEncoder(w).Encode(map[string]any{"message": message, "done": true})
	}))
	defer upstream.Close()
	api := &Server{ollama: ollama.NewClient(upstream.URL)}
	for _, prompt := range []string{titleSystemPrompt, latestTitleSystemPrompt} {
		title, err := api.requestConversationTitle(context.Background(), "always-reasons", prompt, `{}`)
		if err != nil || title != "Input box resizing" {
			t.Fatalf("reasoning exhausted title budget: %q, %v", title, err)
		}
	}
}

func TestTitleFailurePreservesSuccessfulFirstTurn(t *testing.T) {
	for _, result := range []string{"error", "empty", "thinking only"} {
		t.Run(result, func(t *testing.T) {
			fixture := newTitleTestFixture(t, func(w http.ResponseWriter, r *http.Request, isTitle bool) {
				if !isTitle {
					titleTestReply(w, "A successful answer.")
					return
				}
				switch result {
				case "error":
					http.Error(w, "title model unavailable", http.StatusServiceUnavailable)
				case "empty":
					titleTestReply(w, " \n\t ")
				case "thinking only":
					titleTestReply(w, "<think>unfinished reasoning")
				}
			})
			conversation := createTitleTestConversation(t, fixture, "chat")
			response := titleTestMessage(t, fixture, conversation.ID, `{"content":"Question"}`)
			waitForTitleRequest(t, fixture)
			updated := waitForTitleFinished(t, fixture, conversation.ID)
			if updated.Title != "Initial title" || !strings.Contains(response.Body.String(), `"type":"done"`) || strings.Contains(response.Body.String(), `"type":"error"`) {
				t.Fatalf("title failure affected answer or fallback title: %#v %s", updated, response.Body.String())
			}
			messages, err := fixture.database.Messages(context.Background(), conversation.ID)
			if err != nil || len(messages) != 2 || messages[1].Content != "A successful answer." || messages[1].Status != "complete" {
				t.Fatalf("successful answer not preserved: %#v, %v", messages, err)
			}
		})
	}
}

func TestFailedFirstTurnDoesNotRequestTitle(t *testing.T) {
	fixture := newTitleTestFixture(t, func(w http.ResponseWriter, r *http.Request, isTitle bool) {
		http.Error(w, "answer model unavailable", http.StatusServiceUnavailable)
	})
	conversation := createTitleTestConversation(t, fixture, "chat")
	response := titleTestMessage(t, fixture, conversation.ID, `{"content":"Question"}`)
	if !strings.Contains(response.Body.String(), `"type":"error"`) || strings.Contains(response.Body.String(), `"type":"done"`) {
		t.Fatalf("failed response unexpectedly succeeded: %s", response.Body.String())
	}
	fixture.api.titleJobs.Wait()
	select {
	case payload := <-fixture.titles:
		t.Fatalf("failed first turn requested title: %#v", payload)
	default:
	}
	stored, err := fixture.database.GetConversation(context.Background(), conversation.ID)
	if err != nil || stored.AutoTitlePending {
		t.Fatalf("failed first turn left automatic naming pending: %#v, %v", stored, err)
	}
}

func TestCancelledFirstTurnDoesNotRequestTitle(t *testing.T) {
	started := make(chan struct{})
	fixture := newTitleTestFixture(t, func(w http.ResponseWriter, r *http.Request, isTitle bool) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		io.WriteString(w, "{\"message\":{\"role\":\"assistant\",\"content\":\"Partial answer\"},\"done\":false}\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	})
	conversation := createTitleTestConversation(t, fixture, "chat")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/conversations/%d/messages", conversation.ID), strings.NewReader(`{"content":"Question"}`)).WithContext(ctx)
		fixture.api.ServeHTTP(response, request)
		finished <- response
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first response did not start")
	}
	cancel()
	select {
	case response := <-finished:
		if strings.Contains(response.Body.String(), `"type":"done"`) {
			t.Fatalf("cancelled response reported success: %s", response.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled response did not stop")
	}
	fixture.api.titleJobs.Wait()
	select {
	case payload := <-fixture.titles:
		t.Fatalf("cancelled first turn requested title: %#v", payload)
	default:
	}
	stored, err := fixture.database.GetConversation(context.Background(), conversation.ID)
	if err != nil || stored.AutoTitlePending {
		t.Fatalf("cancelled first turn left naming pending: %#v, %v", stored, err)
	}
	history, err := fixture.database.Messages(context.Background(), conversation.ID)
	if err != nil || len(history) != 2 || history[1].Status != "cancelled" {
		t.Fatalf("cancelled first response was not persisted: %#v, %v", history, err)
	}
}

func TestTitleSourceUsesBoundedUTF8TextAttachmentWithoutImages(t *testing.T) {
	fixture := newTitleTestFixture(t, func(w http.ResponseWriter, r *http.Request, isTitle bool) {
		if isTitle {
			titleTestReply(w, "Analyzing an attached report")
			return
		}
		titleTestReply(w, "Report summary: "+strings.Repeat("结果", 2000))
	})
	conversation := createTitleTestConversation(t, fixture, "chat")
	content := "Report begins: " + strings.Repeat("世界", 5000)
	body, _ := json.Marshal(map[string]any{"content": "Summarize this report", "files": []imageInput{{Name: "report.txt", MediaType: "text/plain", Data: base64.StdEncoding.EncodeToString([]byte(content))}}})
	response := titleTestMessage(t, fixture, conversation.ID, string(body))
	if !strings.Contains(response.Body.String(), `"type":"done"`) {
		t.Fatalf("attachment response failed: %s", response.Body.String())
	}
	payload := waitForTitleRequest(t, fixture)
	var source map[string]string
	if err := json.Unmarshal([]byte(payload.Messages[1].Content), &source); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(source["first_user_input"], "Summarize this report") || !strings.Contains(source["first_user_input"], "report.txt") || !strings.Contains(source["first_user_input"], "Report begins:") || utf8.RuneCountInString(source["first_user_input"]) > 2000 || utf8.RuneCountInString(source["first_assistant_reply"]) > 1500 {
		t.Fatalf("title input omitted file excerpt or exceeded budget: %#v", source)
	}
	for _, message := range payload.Messages {
		if !utf8.ValidString(message.Content) || len(message.Images) != 0 || len(message.ToolCalls) != 0 {
			t.Fatalf("title request had invalid UTF-8/images/tool calls: %#v", message)
		}
	}
	waitForTitleFinished(t, fixture, conversation.ID)
}

func TestLateAutomaticTitleCannotReplaceManualRenameOrDeletedSession(t *testing.T) {
	for _, action := range []string{"custom title", "same title", "delete", "delete first turn"} {
		t.Run(action, func(t *testing.T) {
			release := make(chan struct{})
			returned := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			fixture := newTitleTestFixture(t, func(w http.ResponseWriter, r *http.Request, isTitle bool) {
				if !isTitle {
					titleTestReply(w, "First answer")
					return
				}
				defer close(returned)
				select {
				case <-release:
					titleTestReply(w, "Late automatic title")
				case <-r.Context().Done():
				}
			})
			t.Cleanup(unblock)
			conversation := createTitleTestConversation(t, fixture, "chat")
			titleTestMessage(t, fixture, conversation.ID, `{"content":"First question"}`)
			waitForTitleRequest(t, fixture)
			wantTitle := "Initial title"
			method, body := http.MethodPatch, `{"title":"Initial title"}`
			if action == "custom title" {
				wantTitle = "My manual title"
				body = `{"title":"My manual title"}`
			} else if action == "delete" || action == "delete first turn" {
				method, body = http.MethodDelete, ""
			}
			url := fmt.Sprintf("/api/conversations/%d", conversation.ID)
			if action == "delete first turn" {
				history, err := fixture.database.Messages(context.Background(), conversation.ID)
				if err != nil || len(history) != 2 {
					t.Fatalf("first turn missing before removal: %#v, %v", history, err)
				}
				url += fmt.Sprintf("/messages/%d", history[0].ID)
			}
			response := httptest.NewRecorder()
			fixture.api.ServeHTTP(response, httptest.NewRequest(method, url, strings.NewReader(body)))
			if response.Code != http.StatusOK && response.Code != http.StatusNoContent {
				t.Fatalf("manual action failed: %d %s", response.Code, response.Body.String())
			}
			unblock()
			select {
			case <-returned:
			case <-time.After(3 * time.Second):
				t.Fatal("held title response did not finish")
			}
			fixture.api.titleJobs.Wait()
			got, err := fixture.database.GetConversation(context.Background(), conversation.ID)
			if action == "delete" {
				if !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("deleted session was recreated: %#v, %v", got, err)
				}
			} else if err != nil || got.Title != wantTitle || got.AutoTitlePending {
				t.Fatalf("late title replaced manual title: %#v, %v", got, err)
			}
			if action == "delete first turn" {
				response := titleTestMessage(t, fixture, conversation.ID, `{"content":"A replacement first question"}`)
				if !strings.Contains(response.Body.String(), `"type":"done"`) {
					t.Fatalf("replacement turn failed: %s", response.Body.String())
				}
				fixture.api.titleJobs.Wait()
				select {
				case extra := <-fixture.titles:
					t.Fatalf("deleting the original first turn reenabled naming: %#v", extra)
				default:
				}
			}
		})
	}
}

func TestGeneratedTitleCleaning(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"Title: \"Planning a trip\"", "Planning a trip"},
		{"```text\n改善数据库查询\n```", "改善数据库查询"},
		{"<think>Reasoning</think>\n标题： **新会话标题**", "新会话标题"},
		{"<think>Unfinished reasoning", ""},
		{" \n ", ""},
		{strings.Repeat("界", 100), strings.Repeat("界", 80)},
	} {
		if got := cleanConversationTitle(test.input); got != test.want {
			t.Errorf("clean title %q: got %q, want %q", test.input, got, test.want)
		}
	}
}

func TestServerCloseCancelsBackgroundTitleAndFinishesPendingState(t *testing.T) {
	cancelled := make(chan struct{})
	fixture := newTitleTestFixture(t, func(w http.ResponseWriter, r *http.Request, isTitle bool) {
		if !isTitle {
			titleTestReply(w, "Completed answer")
			return
		}
		<-r.Context().Done()
		close(cancelled)
	})
	conversation := createTitleTestConversation(t, fixture, "chat")
	response := titleTestMessage(t, fixture, conversation.ID, `{"content":"First question"}`)
	if !strings.Contains(response.Body.String(), `"type":"done"`) {
		t.Fatalf("first turn failed: %s", response.Body.String())
	}
	waitForTitleRequest(t, fixture)
	closed := make(chan struct{})
	go func() {
		fixture.api.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("server shutdown did not stop background naming")
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("server shutdown did not cancel model title request")
	}
	stored, err := fixture.database.GetConversation(context.Background(), conversation.ID)
	if err != nil || stored.AutoTitlePending || stored.Title != "Initial title" {
		t.Fatalf("shutdown left pending state or overwrote title: %#v, %v", stored, err)
	}
}
