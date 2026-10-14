// Package tavily provides only Tavily Search. Provider credentials are read at
// call time and never included in model tool parameters, results, or errors.
package tavily

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"ollama-webui/internal/agent"
)

const searchEndpoint = "https://api.tavily.com/search"
const responseLimit = 1 << 20
const searchTimeout = 30 * time.Second

type Config struct {
	Enabled bool
	Token   string
}

type Client struct {
	config func(context.Context) (Config, error)
	// Endpoint and HTTP allow isolated test servers. They are never configurable
	// through the UI, API settings, environment, or model tool arguments.
	Endpoint string
	HTTP     *http.Client
}

func NewClient(config func(context.Context) (Config, error)) *Client {
	return &Client{config: config, Endpoint: searchEndpoint, HTTP: &http.Client{Timeout: searchTimeout}}
}

func (client *Client) Enabled(ctx context.Context) bool {
	if client == nil || client.config == nil || ctx.Err() != nil {
		return false
	}
	config, err := client.config(ctx)
	return err == nil && config.Enabled && strings.TrimSpace(config.Token) != ""
}

type searchRequest struct {
	Query             string `json:"query"`
	MaxResults        int    `json:"max_results"`
	SearchDepth       string `json:"search_depth"`
	AutoParameters    bool   `json:"auto_parameters"`
	IncludeAnswer     bool   `json:"include_answer"`
	IncludeRawContent bool   `json:"include_raw_content"`
	IncludeImages     bool   `json:"include_images"`
}

func (client *Client) Search(ctx context.Context, query string, maxResults int) (agent.WebSearchResponse, error) {
	if err := ctx.Err(); err != nil {
		return agent.WebSearchResponse{}, requestError(ctx, err)
	}
	if client == nil || client.config == nil {
		return agent.WebSearchResponse{}, errors.New("web search is unavailable; configure a Tavily token in Settings")
	}
	config, err := client.config(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return agent.WebSearchResponse{}, requestError(ctx, ctx.Err())
		}
		return agent.WebSearchResponse{}, errors.New("could not load web search settings; retry or check server logs")
	}
	token := strings.TrimSpace(config.Token)
	if !config.Enabled || token == "" {
		return agent.WebSearchResponse{}, errors.New("web search is disabled or has no configured token; enable it in Settings")
	}
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 4096 || !utf8.ValidString(query) {
		return agent.WebSearchResponse{}, errors.New("web search query must contain 1 to 4096 bytes of UTF-8 text")
	}
	if maxResults < 1 || maxResults > 10 {
		return agent.WebSearchResponse{}, errors.New("web search max_results must be between 1 and 10")
	}
	payload, err := json.Marshal(searchRequest{Query: query, MaxResults: maxResults, SearchDepth: "basic"})
	if err != nil {
		return agent.WebSearchResponse{}, errors.New("could not prepare web search request")
	}
	requestCtx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	endpoint := client.Endpoint
	if endpoint == "" {
		endpoint = searchEndpoint
	}
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return agent.WebSearchResponse{}, errors.New("could not prepare web search request")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	transport := client.HTTP
	if transport == nil {
		transport = &http.Client{Timeout: searchTimeout}
	}
	// Copy instead of mutating a shared client's redirect settings. Never forward
	// bearer credentials or search queries through provider redirects.
	httpClient := *transport
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := httpClient.Do(request)
	if err != nil {
		return agent.WebSearchResponse{}, requestError(requestCtx, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return agent.WebSearchResponse{}, statusError(response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return agent.WebSearchResponse{}, requestError(requestCtx, err)
	}
	if len(body) > responseLimit {
		return agent.WebSearchResponse{}, errors.New("Tavily returned an oversized search response; try a narrower query")
	}
	var decoded struct {
		Results *[]agent.WebSearchResult `json:"results"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil || decoded.Results == nil {
		return agent.WebSearchResponse{}, errors.New("Tavily returned an invalid search response; retry later")
	}
	result := agent.WebSearchResponse{Query: query, Results: make([]agent.WebSearchResult, 0, maxResults)}
	for _, item := range *decoded.Results {
		if !safeSourceURL(item.URL) || strings.Contains(item.URL, token) {
			continue
		}
		item.Title = strings.ReplaceAll(item.Title, token, "[redacted]")
		item.Content = strings.ReplaceAll(item.Content, token, "[redacted]")
		result.Results = append(result.Results, item)
		if len(result.Results) >= maxResults {
			break
		}
	}
	return result, nil
}

func safeSourceURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || strings.ContainsAny(value, "\r\n\t ") {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func requestError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("web search cancelled: %w", context.Canceled)
	}
	var timeout net.Error
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return fmt.Errorf("web search timed out; retry with a narrower query: %w", context.DeadlineExceeded)
	}
	return errors.New("could not reach Tavily; check the server's internet connection and retry")
}

func statusError(status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return errors.New("Tavily rejected the token; update it in Settings")
	case http.StatusTooManyRequests:
		return errors.New("Tavily rate limit reached; wait before retrying")
	case 432, 433:
		return errors.New("Tavily usage limit reached; check the account's search credits")
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return errors.New("Tavily rejected the search request; try a simpler query")
	default:
		if status >= 300 && status < 400 {
			return errors.New("Tavily returned an unexpected redirect; retry later")
		}
		return fmt.Errorf("Tavily search failed (HTTP %d); retry later", status)
	}
}
