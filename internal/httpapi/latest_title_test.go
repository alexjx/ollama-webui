package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"ollama-webui/internal/agent"
	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

func newLatestTitleFixture(t *testing.T, respond func(http.ResponseWriter, *http.Request)) titleTestFixture {
	t.Helper()
	titles := make(chan titleTestPayload, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload titleTestPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		titles <- payload
		respond(w, r)
	}))
	path := filepath.Join(t.TempDir(), "latest-titles.db")
	database, err := store.Open(context.Background(), path)
	if err != nil {
		upstream.Close()
		t.Fatal(err)
	}
	client := ollama.NewClient(upstream.URL)
	api := New(database, client, agent.Runner{}, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { api.Close(); upstream.Close(); database.Close() })
	return titleTestFixture{api: api, database: database, path: path, titles: titles}
}

func addLatestTitleMessage(t *testing.T, fixture titleTestFixture, id int64, role, content, status string, metadata map[string]any) store.Message {
	t.Helper()
	message, err := fixture.database.AddMessage(context.Background(), id, role, content, status)
	if err != nil {
		t.Fatal(err)
	}
	if metadata != nil {
		if err := fixture.database.UpdateMessage(context.Background(), message.ID, content, "PRIVATE_THINKING", status, metadata); err != nil {
			t.Fatal(err)
		}
	}
	return message
}

func latestTitleRequest(t *testing.T, fixture titleTestFixture, id int64) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	fixture.api.ServeHTTP(response, httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/conversations/%d/title", id), nil))
	return response
}

func TestLatestTitleUsesLatestCompletedInteractionAndFinalAnswer(t *testing.T) {
	for _, mode := range []string{"chat", "agent"} {
		for _, pending := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/pending=%t", mode, pending), func(t *testing.T) {
				fixture := newLatestTitleFixture(t, func(w http.ResponseWriter, r *http.Request) { titleTestReply(w, "最新会话标题") })
				conversation := createTitleTestConversation(t, fixture, mode)
				if !pending {
					if err := fixture.database.UpdateTitle(context.Background(), conversation.ID, "Manual title"); err != nil {
						t.Fatal(err)
					}
				}
				addLatestTitleMessage(t, fixture, conversation.ID, "user", "OLD_QUESTION", "complete", nil)
				addLatestTitleMessage(t, fixture, conversation.ID, "assistant", "OLD_ANSWER", "complete", nil)
				addLatestTitleMessage(t, fixture, conversation.ID, "user", "Latest question", "complete", nil)
				addLatestTitleMessage(t, fixture, conversation.ID, "system", "PRIVATE_SYSTEM_CONTEXT", "complete", nil)
				prefix := "PRIVATE_PROGRESS_正在调查\n\n"
				reply := addLatestTitleMessage(t, fixture, conversation.ID, "assistant", prefix+"最终答案", "complete", map[string]any{"final_answer_start": len(prefix), "response_model": "answer-model"})
				step, err := fixture.database.BeginAgentStep(context.Background(), reply.ID, 1, "shell", `{"command":"PRIVATE_TOOL_INPUT"}`)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.database.CompleteAgentStep(context.Background(), step.ID, "PRIVATE_TOOL_OUTPUT", 0, "complete"); err != nil {
					t.Fatal(err)
				}
				addLatestTitleMessage(t, fixture, conversation.ID, "user", "FAILED_QUESTION", "complete", nil)
				addLatestTitleMessage(t, fixture, conversation.ID, "assistant", "FAILED_REPLY", "error", nil)
				addLatestTitleMessage(t, fixture, conversation.ID, "user", "CANCELLED_QUESTION", "complete", nil)
				addLatestTitleMessage(t, fixture, conversation.ID, "assistant", "CANCELLED_REPLY", "cancelled", nil)
				before, err := fixture.database.Messages(context.Background(), conversation.ID)
				if err != nil {
					t.Fatal(err)
				}
				response := latestTitleRequest(t, fixture, conversation.ID)
				if response.Code != http.StatusOK {
					t.Fatalf("title request: %d %s", response.Code, response.Body.String())
				}
				payload := waitForTitleRequest(t, fixture)
				var source map[string]string
				if err := json.Unmarshal([]byte(payload.Messages[1].Content), &source); err != nil {
					t.Fatal(err)
				}
				if payload.Model != "answer-model" || payload.Messages[0].Content != latestTitleSystemPrompt || len(payload.Messages) != 2 || len(payload.Tools) != 0 || len(payload.Think) != 0 || source["latest_user_input"] != "Latest question" || source["latest_assistant_reply"] != "最终答案" {
					t.Fatalf("wrong title source/model: %#v %#v", payload, source)
				}
				var updated store.Conversation
				if err := json.Unmarshal(response.Body.Bytes(), &updated); err != nil || updated.Title != "最新会话标题" || updated.AutoTitlePending {
					t.Fatalf("wrong updated session: %#v %v", updated, err)
				}
				after, err := fixture.database.Messages(context.Background(), conversation.ID)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("title changed transcript: %v", err)
				}
			})
		}
	}
}

func TestLatestTitleSupportsLegacyRepliesAndAttachmentOnlyQuestions(t *testing.T) {
	fixture := newLatestTitleFixture(t, func(w http.ResponseWriter, r *http.Request) { titleTestReply(w, "File review") })
	conversation := createTitleTestConversation(t, fixture, "agent")
	_, err := fixture.database.AddMessageWithAttachments(context.Background(), conversation.ID, "user", "", "complete", []store.NewAttachment{{FileName: "notes.txt", MediaType: "text/plain", Data: []byte("Latest attached question")}})
	if err != nil {
		t.Fatal(err)
	}
	addLatestTitleMessage(t, fixture, conversation.ID, "assistant", "Legacy progress and final answer", "complete", nil)
	response := latestTitleRequest(t, fixture, conversation.ID)
	if response.Code != http.StatusOK {
		t.Fatalf("title request: %d %s", response.Code, response.Body.String())
	}
	payload := waitForTitleRequest(t, fixture)
	if payload.Model != conversation.Model || !strings.Contains(payload.Messages[1].Content, "Latest attached question") || !strings.Contains(payload.Messages[1].Content, "Legacy progress and final answer") || !strings.Contains(payload.Messages[0].Content, "ignore any earlier progress narration") {
		t.Fatalf("legacy source not supported: %#v", payload)
	}
}

func TestLatestTitleReadsBoundedSelectedTextAttachmentPrefixes(t *testing.T) {
	fixture := newLatestTitleFixture(t, func(w http.ResponseWriter, r *http.Request) { titleTestReply(w, "Latest file review") })
	conversation := createTitleTestConversation(t, fixture, "chat")
	ctx := context.Background()
	_, err := fixture.database.AddMessageWithAttachments(ctx, conversation.ID, "user", "OLD_QUESTION", "complete", []store.NewAttachment{{FileName: "old.txt", MediaType: "text/plain", Data: []byte(strings.Repeat("OLD_FILE_CONTENT", 10000))}})
	if err != nil {
		t.Fatal(err)
	}
	addLatestTitleMessage(t, fixture, conversation.ID, "assistant", "Old answer", "complete", nil)
	_, err = fixture.database.AddMessageWithAttachments(ctx, conversation.ID, "user", "Latest files", "complete", []store.NewAttachment{
		{FileName: "image.png", MediaType: "image/png", Data: []byte(strings.Repeat("PRIVATE_IMAGE_BYTES", 10000))},
		{FileName: "short.txt", MediaType: "text/plain", Data: []byte("前缀a")},
		{FileName: "long.txt", MediaType: "text/plain", Data: []byte("b" + strings.Repeat("🌲", 10000) + "PRIVATE_FILE_SUFFIX")},
		{FileName: "unused.txt", MediaType: "text/plain", Data: []byte("PRIVATE_UNUSED_FILE")},
	})
	if err != nil {
		t.Fatal(err)
	}
	addLatestTitleMessage(t, fixture, conversation.ID, "assistant", "Latest answer", "complete", nil)
	history, err := fixture.database.Messages(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	user, _, ok := latestTitleInteraction(history)
	if !ok {
		t.Fatal("latest pair missing")
	}
	excerpts, err := fixture.api.titleAttachmentExcerpts(ctx, conversation.ID, user)
	if err != nil {
		t.Fatal(err)
	}
	if len(excerpts.Attachments[0].Data) != 0 || string(excerpts.Attachments[1].Data) != "前缀a" || utf8.RuneCount(excerpts.Attachments[2].Data) != 997 || !utf8.Valid(excerpts.Attachments[2].Data) || len(excerpts.Attachments[3].Data) != 0 {
		t.Fatalf("unexpected excerpts: %#v", excerpts.Attachments)
	}
	for _, attachment := range user.Attachments {
		if len(attachment.Data) != 0 {
			t.Fatal("excerpt loading mutated metadata history")
		}
	}
	response := latestTitleRequest(t, fixture, conversation.ID)
	if response.Code != 200 {
		t.Fatalf("latest title failed: %d %s", response.Code, response.Body.String())
	}
	payload := waitForTitleRequest(t, fixture)
	var source map[string]string
	if err := json.Unmarshal([]byte(payload.Messages[1].Content), &source); err != nil {
		t.Fatal(err)
	}
	question := source["latest_user_input"]
	if !strings.Contains(question, "前缀a") || strings.Count(question, "🌲") != 996 || strings.Contains(question, "PRIVATE_") || strings.Contains(question, "OLD_FILE_CONTENT") {
		t.Fatalf("attachment budget/source violated: %s", question)
	}
}

func TestLatestTitleRejectsMissingHistoryAndInvalidIDs(t *testing.T) {
	fixture := newLatestTitleFixture(t, func(w http.ResponseWriter, r *http.Request) { titleTestReply(w, "Unexpected title") })
	conversation := createTitleTestConversation(t, fixture, "chat")
	for _, test := range []struct {
		path   string
		status int
	}{
		{"/api/conversations/bad/title", 400}, {"/api/conversations/0/title", 400}, {"/api/conversations/999/title", 404}, {fmt.Sprintf("/api/conversations/%d/title", conversation.ID), 422},
	} {
		response := httptest.NewRecorder()
		fixture.api.ServeHTTP(response, httptest.NewRequest(http.MethodPost, test.path, nil))
		if response.Code != test.status {
			t.Fatalf("%s: %d %s", test.path, response.Code, response.Body.String())
		}
	}
	addLatestTitleMessage(t, fixture, conversation.ID, "user", "No successful reply", "complete", nil)
	addLatestTitleMessage(t, fixture, conversation.ID, "assistant", "Partial output", "error", nil)
	if response := latestTitleRequest(t, fixture, conversation.ID); response.Code != 422 {
		t.Fatalf("failed turn accepted: %d", response.Code)
	}
	stored, err := fixture.database.GetConversation(context.Background(), conversation.ID)
	if err != nil || stored.Title != conversation.Title || !stored.AutoTitlePending {
		t.Fatalf("invalid action changed title eligibility: %#v %v", stored, err)
	}
	if len(fixture.titles) != 0 || len(fixture.api.active) != 0 {
		t.Fatal("invalid action started model or left reservation")
	}
}

func TestLatestTitleFailurePreservesCurrentName(t *testing.T) {
	for _, result := range []string{"error", "empty", "thinking", "oversized"} {
		t.Run(result, func(t *testing.T) {
			fixture := newLatestTitleFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch result {
				case "error":
					http.Error(w, "model unavailable", 503)
				case "empty":
					titleTestReply(w, " \n ")
				case "thinking":
					titleTestReply(w, "<think>reasoning only")
				case "oversized":
					titleTestReply(w, strings.Repeat("a", 2049))
				}
			})
			conversation := createTitleTestConversation(t, fixture, "chat")
			addLatestTitleMessage(t, fixture, conversation.ID, "user", "Question", "complete", nil)
			addLatestTitleMessage(t, fixture, conversation.ID, "assistant", "Answer", "complete", nil)
			response := latestTitleRequest(t, fixture, conversation.ID)
			stored, err := fixture.database.GetConversation(context.Background(), conversation.ID)
			if response.Code != 502 || err != nil || stored.Title != conversation.Title || stored.AutoTitlePending || len(fixture.api.active) != 0 {
				t.Fatalf("failure changed title or leaked reservation: %d %#v %v", response.Code, stored, err)
			}
		})
	}
}

func TestLatestTitleSupersedesPendingAutomaticTitle(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	fixture := newTitleTestFixture(t, func(w http.ResponseWriter, r *http.Request, isTitle bool) {
		if isTitle {
			select {
			case <-release:
				titleTestReply(w, "Outdated first-turn title")
			case <-r.Context().Done():
			}
			return
		}
		titleTestReply(w, "Latest interaction title")
	})
	t.Cleanup(unblock)
	conversation := createTitleTestConversation(t, fixture, "chat")
	first := addLatestTitleMessage(t, fixture, conversation.ID, "user", "First question", "complete", nil)
	addLatestTitleMessage(t, fixture, conversation.ID, "assistant", "First answer", "complete", nil)
	addLatestTitleMessage(t, fixture, conversation.ID, "user", "Latest question", "complete", nil)
	addLatestTitleMessage(t, fixture, conversation.ID, "assistant", "Latest answer", "complete", nil)
	fixture.api.startConversationTitle(conversation, first, "First answer")
	waitForTitleRequest(t, fixture)
	response := latestTitleRequest(t, fixture, conversation.ID)
	if response.Code != 200 {
		t.Fatalf("latest naming failed: %d %s", response.Code, response.Body.String())
	}
	unblock()
	fixture.api.titleJobs.Wait()
	stored, err := fixture.database.GetConversation(context.Background(), conversation.ID)
	if err != nil || stored.Title != "Latest interaction title" || stored.AutoTitlePending {
		t.Fatalf("late automatic title overwrote explicit title: %#v %v", stored, err)
	}
}

func TestLatestTitleRejectsExistingGenerationReservation(t *testing.T) {
	fixture := newLatestTitleFixture(t, func(w http.ResponseWriter, r *http.Request) { titleTestReply(w, "Unexpected title") })
	conversation := createTitleTestConversation(t, fixture, "chat")
	fixture.api.active[conversation.ID] = struct{}{}
	response := latestTitleRequest(t, fixture, conversation.ID)
	if response.Code != 409 || len(fixture.titles) != 0 || len(fixture.api.active) != 1 {
		t.Fatalf("active action was started or reservation lost: %d %s", response.Code, response.Body.String())
	}
}

func TestLatestTitleReservationCancellationAndConcurrentRename(t *testing.T) {
	for _, outcome := range []string{"success", "cancel", "rename", "delete"} {
		t.Run(outcome, func(t *testing.T) {
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			fixture := newLatestTitleFixture(t, func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-release:
					titleTestReply(w, "Generated title")
				case <-r.Context().Done():
				}
			})
			t.Cleanup(unblock)
			conversation := createTitleTestConversation(t, fixture, "chat")
			addLatestTitleMessage(t, fixture, conversation.ID, "user", "Question", "complete", nil)
			addLatestTitleMessage(t, fixture, conversation.ID, "assistant", "Answer", "complete", nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			url := fmt.Sprintf("/api/conversations/%d", conversation.ID)
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				defer close(done)
				fixture.api.ServeHTTP(response, httptest.NewRequest(http.MethodPost, url+"/title", nil).WithContext(ctx))
			}()
			waitForTitleRequest(t, fixture)
			for _, path := range []string{url + "/title", url + "/messages", url + "/compact"} {
				busy := httptest.NewRecorder()
				fixture.api.ServeHTTP(busy, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"content":"Blocked"}`)))
				if busy.Code != 409 {
					t.Fatalf("%s not blocked: %d %s", path, busy.Code, busy.Body.String())
				}
			}
			switch outcome {
			case "cancel":
				cancel()
			case "rename":
				rename := httptest.NewRecorder()
				fixture.api.ServeHTTP(rename, httptest.NewRequest(http.MethodPatch, url, strings.NewReader(`{"title":"My manual title"}`)))
				if rename.Code != 200 {
					t.Fatalf("rename blocked: %d", rename.Code)
				}
			case "delete":
				if err := fixture.database.DeleteConversation(context.Background(), conversation.ID); err != nil {
					t.Fatal(err)
				}
			}
			unblock()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("title action did not finish")
			}
			if len(fixture.api.active) != 0 {
				t.Fatal("title reservation leaked")
			}
			if outcome == "delete" {
				if response.Code != 404 {
					t.Fatalf("deleted session returned %d %s", response.Code, response.Body.String())
				}
				return
			}
			if outcome == "rename" && response.Code != 409 {
				t.Fatalf("concurrent rename returned %d %s", response.Code, response.Body.String())
			}
			stored, err := fixture.database.GetConversation(context.Background(), conversation.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := "Generated title"
			if outcome == "rename" {
				want = "My manual title"
			}
			if outcome == "cancel" {
				want = conversation.Title
			}
			if stored.Title != want {
				t.Fatalf("%s: title %q want %q", outcome, stored.Title, want)
			}
		})
	}
}

func TestFinalTitleReplyValidatesUTF8ByteOffsets(t *testing.T) {
	for _, test := range []struct {
		offset any
		want   string
	}{
		{0, "调查Answer"}, {len("调查"), "Answer"}, {float64(len("调查")), "Answer"}, {len("调查Answer"), ""}, {1, "调查Answer"}, {-1, "调查Answer"}, {1000, "调查Answer"}, {1.5, "调查Answer"}, {"6", "调查Answer"}, {nil, "调查Answer"},
	} {
		if got := finalTitleReply(store.Message{Content: "调查Answer", Metadata: map[string]any{"final_answer_start": test.offset}}); got != test.want {
			t.Fatalf("offset %v: got %q want %q", test.offset, got, test.want)
		}
	}
}
