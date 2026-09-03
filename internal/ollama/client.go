package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{baseURL: baseURL, http: &http.Client{Timeout: 0}}
}

type Model struct {
	Name         string         `json:"name"`
	ModifiedAt   time.Time      `json:"modified_at"`
	Size         int64          `json:"size"`
	Digest       string         `json:"digest"`
	Details      map[string]any `json:"details,omitempty"`
	Capabilities []string       `json:"capabilities,omitempty"`
}

func (c *Client) ListModels(ctx context.Context) ([]Model, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("contact Ollama: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, responseError(response)
	}
	var payload struct {
		Models []Model `json:"models"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode Ollama models: %w", err)
	}
	if payload.Models == nil {
		payload.Models = make([]Model, 0)
	}
	models := make([]Model, 0, len(payload.Models))
	for _, model := range payload.Models {
		if len(model.Capabilities) == 0 || hasCapability(model.Capabilities, "completion") {
			models = append(models, model)
		}
	}
	return models, nil
}

func hasCapability(capabilities []string, wanted string) bool {
	for _, capability := range capabilities {
		if capability == wanted {
			return true
		}
	}
	return false
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Thinking   string     `json:"thinking,omitempty"`
	Images     [][]byte   `json:"images,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolName   string     `json:"tool_name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string           `json:"id,omitempty"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Index     int             `json:"index"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ChatOptions struct {
	NumCtx      *int     `json:"num_ctx,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
}

type ChatRequest struct {
	Model    string       `json:"model"`
	Messages []Message    `json:"messages"`
	Tools    []Tool       `json:"tools,omitempty"`
	Options  *ChatOptions `json:"options,omitempty"`
	Think    *bool        `json:"think,omitempty"`
	Stream   bool         `json:"stream"`
}

type ChatChunk struct {
	Error              string  `json:"error,omitempty"`
	Model              string  `json:"model,omitempty"`
	Message            Message `json:"message,omitempty"`
	Done               bool    `json:"done"`
	DoneReason         string  `json:"done_reason,omitempty"`
	TotalDuration      int64   `json:"total_duration,omitempty"`
	LoadDuration       int64   `json:"load_duration,omitempty"`
	PromptEvalCount    int     `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64   `json:"prompt_eval_duration,omitempty"`
	EvalCount          int     `json:"eval_count,omitempty"`
	EvalDuration       int64   `json:"eval_duration,omitempty"`
}

func (c *Client) Chat(ctx context.Context, input ChatRequest, onChunk func(ChatChunk) error) error {
	input.Stream = true
	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode Ollama chat request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("contact Ollama: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return responseError(response)
	}
	decoder := json.NewDecoder(response.Body)
	done := false
	for {
		var chunk ChatChunk
		if err := decoder.Decode(&chunk); errors.Is(err, io.EOF) {
			if !done {
				return errors.New("Ollama stream ended before completion")
			}
			return nil
		} else if err != nil {
			return fmt.Errorf("decode Ollama stream: %w", err)
		}
		if chunk.Error != "" {
			return errors.New(chunk.Error)
		}
		if err := onChunk(chunk); err != nil {
			return err
		}
		if chunk.Done {
			done = true
		}
	}
}

func responseError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.Error != "" {
		return fmt.Errorf("Ollama returned %s: %s", response.Status, payload.Error)
	}
	return fmt.Errorf("Ollama returned %s", response.Status)
}
