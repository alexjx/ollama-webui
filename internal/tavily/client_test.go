package tavily

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testToken = "tvly-test-private-token"

func configuredClient() *Client {
	return NewClient(func(context.Context) (Config, error) { return Config{Enabled: true, Token: testToken}, nil })
}

func TestSearchUsesBearerTokenAndSearchOnlyParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/search" || r.Header.Get("Authorization") != "Bearer "+testToken || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("incorrect search request: %s %s, headers=%v", r.Method, r.URL, r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		want := map[string]any{"query": "current weather", "max_results": float64(2), "search_depth": "basic", "auto_parameters": false,
			"include_answer": false, "include_raw_content": false, "include_images": false}
		actual, _ := json.Marshal(body)
		expected, _ := json.Marshal(want)
		if string(actual) != string(expected) || strings.Contains(string(actual), testToken) {
			t.Errorf("unexpected provider body: %s", actual)
		}
		io.WriteString(w, `{"query":"provider rewritten query","answer":"must not expose answer","images":["ignored"],"results":[{"title":"Weather","url":"https://example.com/weather","content":"Rain","score":0.9,"raw_content":"ignored raw content"}]}`)
	}))
	defer server.Close()
	client := configuredClient()
	if client.Endpoint != "https://api.tavily.com/search" {
		t.Fatal("default endpoint changed")
	}
	client.Endpoint = server.URL + "/search"
	result, err := client.Search(context.Background(), "  current weather  ", 2)
	if err != nil || result.Query != "current weather" || len(result.Results) != 1 || result.Results[0].Content != "Rain" {
		t.Fatalf("unexpected result: %#v, %v", result, err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "ignored") || strings.Contains(string(encoded), "must not expose") {
		t.Fatalf("non-search response fields escaped: %s", encoded)
	}
}

func TestSearchFiltersUnsafeURLsAndRedactsToken(t *testing.T) {
	client := configuredClient()
	client.HTTP = &http.Client{Transport: responseTransport(`{"results":[
		{"title":"bad","url":"javascript:alert(1)"},
		{"url":"file:///etc/passwd"},
		{"url":"//example.com"},
		{"url":"https://user:password@example.com"},
		{"url":"https://example.com/?token=` + testToken + `"},
		{"title":"` + testToken + `","url":"https://example.com/a","content":"Secret ` + testToken + `"},
		{"url":"http://example.com/b"},
		{"url":"https://example.com/c"}]}`)}
	result, err := client.Search(context.Background(), "query", 2)
	if err != nil || len(result.Results) != 2 || result.Results[0].URL != "https://example.com/a" || result.Results[1].URL != "http://example.com/b" {
		t.Fatalf("unsafe sources accepted or limit ignored: %#v, %v", result, err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), testToken) || !strings.Contains(string(encoded), "[redacted]") {
		t.Fatalf("provider token reflection not redacted: %s", encoded)
	}
}

func TestSearchRechecksSettingsForEveryCall(t *testing.T) {
	settings := Config{Enabled: true, Token: testToken}
	client := NewClient(func(context.Context) (Config, error) { return settings, nil })
	calls := 0
	client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return responseTransport(`{"results":[]}`).RoundTrip(r)
	})}
	if !client.Enabled(context.Background()) {
		t.Fatal("configured search disabled")
	}
	settings.Enabled = false
	if _, err := client.Search(context.Background(), "query", 5); err == nil || calls != 0 || client.Enabled(context.Background()) {
		t.Fatalf("disabled setting ignored: %d %v", calls, err)
	}
	settings.Enabled = true
	settings.Token = " "
	if _, err := client.Search(context.Background(), "query", 5); err == nil || calls != 0 || client.Enabled(context.Background()) {
		t.Fatalf("missing token ignored: %d %v", calls, err)
	}
	settings.Token = testToken
	result, err := client.Search(context.Background(), "query", 5)
	if err != nil || calls != 1 || result.Results == nil || len(result.Results) != 0 {
		t.Fatalf("empty result was not an array: %#v %d %v", result, calls, err)
	}
}

func TestSearchErrorsNeverEchoTokenOrProviderBody(t *testing.T) {
	for _, status := range []int{400, 401, 403, 422, 429, 432, 433, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client := configuredClient()
			client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(testToken + " reflected-private-error")), Header: make(http.Header)}, nil
			})}
			_, err := client.Search(context.Background(), "query", 5)
			if err == nil || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "reflected-private-error") {
				t.Fatalf("unsafe provider error: %v", err)
			}
		})
	}
	client := NewClient(func(context.Context) (Config, error) { return Config{}, errors.New(testToken) })
	if client.Enabled(context.Background()) {
		t.Fatal("unavailable settings advertised")
	}
	_, err := client.Search(context.Background(), "query", 5)
	if err == nil || strings.Contains(err.Error(), testToken) {
		t.Fatalf("settings error echoed token: %v", err)
	}
	client = configuredClient()
	client.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New(testToken) })}
	_, err = client.Search(context.Background(), "query", 5)
	if err == nil || strings.Contains(err.Error(), testToken) {
		t.Fatalf("transport error echoed token: %v", err)
	}
}

func TestSearchRejectsRedirectsWithoutForwardingToken(t *testing.T) {
	redirectRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			http.Redirect(w, r, "/redirect", http.StatusTemporaryRedirect)
			return
		}
		redirectRequests++
		io.WriteString(w, `{"results":[]}`)
	}))
	defer server.Close()
	client := configuredClient()
	client.Endpoint = server.URL + "/search"
	_, err := client.Search(context.Background(), "query", 5)
	if err == nil || !strings.Contains(err.Error(), "redirect") || redirectRequests != 0 {
		t.Fatalf("redirect followed: requests=%d error=%v", redirectRequests, err)
	}
}

func TestSearchCancellationAndTimeout(t *testing.T) {
	for _, cancelled := range []bool{true, false} {
		client := configuredClient()
		started := make(chan struct{})
		client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			deadline, ok := r.Context().Deadline()
			if !ok || time.Until(deadline) > 30*time.Second {
				t.Error("search has no bounded deadline")
			}
			close(started)
			<-r.Context().Done()
			return nil, r.Context().Err()
		})}
		ctx := context.Background()
		var cancel context.CancelFunc
		if cancelled {
			ctx, cancel = context.WithCancel(ctx)
		} else {
			ctx, cancel = context.WithTimeout(ctx, 20*time.Millisecond)
		}
		done := make(chan error, 1)
		go func() { _, err := client.Search(ctx, "query", 5); done <- err }()
		<-started
		want := context.DeadlineExceeded
		if cancelled {
			want = context.Canceled
			cancel()
		}
		select {
		case err := <-done:
			if !errors.Is(err, want) {
				t.Fatalf("wrong interrupted error: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("search did not stop")
		}
		cancel()
	}
}

func TestSearchRejectsInvalidAndOversizedResponses(t *testing.T) {
	for _, body := range []string{testToken, `{}`, `null`, `{"results":null}`, `{"results":"wrong"}`, `{"results":[]} {}`, strings.Repeat("x", responseLimit+1)} {
		client := configuredClient()
		client.HTTP = &http.Client{Transport: responseTransport(body)}
		_, err := client.Search(context.Background(), "query", 5)
		if err == nil || strings.Contains(err.Error(), testToken) {
			t.Fatalf("invalid response accepted or reflected: %v", err)
		}
	}
}

func TestSearchRejectsInvalidInputWithoutCallingProvider(t *testing.T) {
	client := configuredClient()
	client.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid input called provider")
		return nil, errors.New("unexpected call")
	})}
	for _, query := range []string{"", " ", strings.Repeat("x", 4097), string([]byte{0xff})} {
		if _, err := client.Search(context.Background(), query, 5); err == nil {
			t.Fatal("invalid query accepted")
		}
	}
	for _, limit := range []int{0, -1, 11} {
		if _, err := client.Search(context.Background(), "query", limit); err == nil {
			t.Fatal("invalid result count accepted")
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return roundTrip(r)
}

func responseTransport(body string) http.RoundTripper {
	return roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
}
