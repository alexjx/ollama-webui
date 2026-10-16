package httpapi

import (
	"encoding/json"
	"fmt"
	"ollama-webui/internal/store"
	"strings"
	"testing"
)

func TestTemplateAPICRUDAndValidation(t *testing.T) {
	_, api, _ := searchTestAPI(t, "")
	if response := searchAPIRequest(api, "GET", "/api/templates", ""); response.Code != 200 || strings.TrimSpace(response.Body.String()) != `{"items":[]}` {
		t.Fatalf("list: %s", response.Body.String())
	}
	for _, body := range []string{
		`null`, `{}`, `{"name":"   ","message":"hello"}`, `{"name":"x"}`, `{"name":"x","message":"hello","unknown":true}`,
		`{"name":"x","message":"hello"} {}`, `{"name":"x","message":"hello","settings":[]}`,
		`{"name":"x","message":"hello","settings":{"unknown":1}}`,
	} {
		if response := searchAPIRequest(api, "POST", "/api/templates", body); response.Code != 400 {
			t.Errorf("accepted %s: %d %s", body, response.Code, response.Body.String())
		}
	}
	for _, setting := range []string{`"mode":"other"`, `"mode":null`, `"model":" "`, `"model":null`, `"context_window":0`, `"context_window":1.5`, `"context_window":"42"`, `"thinking_mode":"auto"`, `"temperature":2.1`, `"temperature":-1`, `"web_search_enabled":null`, `"web_search_enabled":"true"`} {
		body := `{"name":"x","message":"hello","settings":{` + setting + `}}`
		if response := searchAPIRequest(api, "POST", "/api/templates", body); response.Code != 400 {
			t.Errorf("accepted %s: %s", setting, response.Body.String())
		}
	}
	oversized, _ := json.Marshal(store.TemplateInput{Name: "x", Message: strings.Repeat("a", (128<<10)+1)})
	if response := searchAPIRequest(api, "POST", "/api/templates", string(oversized)); response.Code != 400 {
		t.Fatal("oversized prompt accepted")
	}
	huge := `{"name":"x","message":"hello","description":"` + strings.Repeat(" ", 2<<20) + `"}`
	if response := searchAPIRequest(api, "POST", "/api/templates", huge); response.Code != 400 {
		t.Fatal("oversized body accepted")
	}
	body := `{"name":"Review","message":"  hello\n","system_prompt":" precise ","settings":{"model":"not-installed:portable","mode":"chat","temperature":null,"context_window":null,"thinking_mode":null,"web_search_enabled":false}}`
	response := searchAPIRequest(api, "POST", "/api/templates", body)
	var item store.Template
	if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil || response.Code != 201 || item.Message != "  hello\n" {
		t.Fatalf("create: %d %s %v", response.Code, response.Body.String(), err)
	}
	path := fmt.Sprintf("/api/templates/%d", item.ID)
	response = searchAPIRequest(api, "PUT", path, `{"name":"Instructions","system_prompt":"Be concise"}`)
	if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil || response.Code != 200 || item.Message != "" || item.Settings != nil {
		t.Fatalf("update: %s %v", response.Body.String(), err)
	}
	if response = searchAPIRequest(api, "DELETE", "/api/conversations", `{"confirmation":"DELETE"}`); response.Code != 200 {
		t.Fatalf("clear: %s", response.Body.String())
	}
	if response = searchAPIRequest(api, "GET", "/api/templates", ""); !strings.Contains(response.Body.String(), "Instructions") {
		t.Fatalf("clear removed template: %s", response.Body.String())
	}
	if response = searchAPIRequest(api, "DELETE", path, ""); response.Code != 204 {
		t.Fatalf("delete: %s", response.Body.String())
	}
	for _, method := range []string{"PUT", "DELETE"} {
		if response = searchAPIRequest(api, method, path, body); response.Code != 404 {
			t.Fatalf("missing %s: %s", method, response.Body.String())
		}
		if response = searchAPIRequest(api, method, "/api/templates/0", body); response.Code != 400 {
			t.Fatalf("invalid id %s: %s", method, response.Body.String())
		}
	}
}
