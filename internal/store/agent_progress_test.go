package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func progressTestRun(t *testing.T, db *Store) AgentRun {
	t.Helper()
	ctx := context.Background()
	conversation, err := db.CreateConversation(ctx, CreateConversationParams{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	message, err := db.AddMessage(ctx, conversation.ID, "assistant", "", "streaming")
	if err != nil {
		t.Fatal(err)
	}
	job, err := db.CreateAgentJob(ctx, CreateAgentJobParams{ConversationID: conversation.ID, RequestingMessageID: message.ID, Task: "focused"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.StartAgentJob(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	run, err := db.CreateAgentRun(ctx, CreateAgentRunParams{JobID: job.ID, Model: "test", ContextWindow: 1024, Task: "focused"})
	if err != nil {
		t.Fatal(err)
	}
	run, err = db.StartAgentRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestAgentRunProgressBoundsPersistAndTerminalize(t *testing.T) {
	for _, terminal := range []string{AgentStatusComplete, AgentStatusFailed, AgentStatusCancelled, "recovered"} {
		t.Run(terminal, func(t *testing.T) {
			ctx := context.Background()
			db, err := Open(ctx, filepath.Join(t.TempDir(), "progress.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			run := progressTestRun(t, db)
			if run.Progress != nil {
				t.Fatal("legacy-style run invented activity")
			}
			progress := &AgentRunProgress{Phase: "shell", Turn: 2, Total: 20, Processed: 5}
			for i := 1; i <= 20; i++ {
				progress.Activities = append(progress.Activities, AgentRunActivity{ID: int64(i), ToolName: "shell", Title: strings.Repeat("世界", 100), Detail: strings.Repeat("d", 700), Output: strings.Repeat("输出", 1000) + string([]byte{0xff}), Status: AgentStatusRunning, StartedAt: time.Now().UTC()})
			}
			run, err = db.UpdateAgentRunProgress(ctx, run.ID, progress)
			if err != nil {
				t.Fatal(err)
			}
			if len(run.Progress.Activities) != 12 || run.Progress.Activities[0].ID != 9 || run.Progress.Total != 20 || run.Progress.Processed != 5 {
				t.Fatalf("wrong bounded snapshot: %#v", run.Progress)
			}
			for _, activity := range run.Progress.Activities {
				if len(activity.Title) > 180 || len(activity.Detail) > 500 || len(activity.Output) > 2000 || !utf8.ValidString(activity.Output) {
					t.Fatalf("unbounded/nontext activity: %#v", activity)
				}
			}
			wantStatus := terminal
			switch terminal {
			case AgentStatusComplete:
				run, err = db.CompleteAgentRun(ctx, run.ID, "done", nil)
			case AgentStatusFailed:
				run, err = db.FailAgentRun(ctx, run.ID, "failure")
			case AgentStatusCancelled:
				run, err = db.CancelAgentRun(ctx, run.ID, "stopped")
			case "recovered":
				_, err = db.RecoverStaleAgentWork(ctx, time.Now().UTC().Add(time.Second))
				if err == nil {
					run, err = db.GetAgentRun(ctx, run.ID)
				}
				wantStatus = AgentStatusFailed
			}
			if err != nil {
				t.Fatal(err)
			}
			if run.Progress.Phase != wantStatus {
				t.Fatalf("terminal phase missing: %#v", run.Progress)
			}
			var encoded string
			if err := db.db.QueryRowContext(ctx, `SELECT progress_json FROM agent_runs WHERE id = ?`, run.ID).Scan(&encoded); err != nil {
				t.Fatal(err)
			}
			var persisted AgentRunProgress
			if err := json.Unmarshal([]byte(encoded), &persisted); err != nil {
				t.Fatal(err)
			}
			for _, activity := range persisted.Activities {
				if activity.Status != wantStatus || activity.CompletedAt == nil {
					t.Fatalf("terminal activity not persisted: %#v", activity)
				}
			}
			if _, err := db.UpdateAgentRunProgress(ctx, run.ID, progress); !errors.Is(err, ErrInvalidAgentState) {
				t.Fatalf("late progress revived terminal run: %v", err)
			}
		})
	}
}

func TestLegacyAgentRunProgressMigrationLeavesHistoryEmpty(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "legacy.db")
	db, err := Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	run := progressTestRun(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	legacy, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.ExecContext(ctx, `ALTER TABLE agent_runs DROP COLUMN progress_json`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	loaded, err := db.GetAgentRun(ctx, run.ID)
	if err != nil || loaded.Progress != nil {
		t.Fatalf("legacy history gained invented activity: %#v, %v", loaded.Progress, err)
	}
}
