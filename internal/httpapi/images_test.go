package httpapi

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ollama-webui/internal/ollama"
)

var onePixelPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")

func TestDecodeImagesAcceptsVerifiedPNG(t *testing.T) {
	attachments, err := decodeImages([]imageInput{{
		Name: "pixel.png", MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(onePixelPNG),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 || attachments[0].MediaType != "image/png" || string(attachments[0].Data) != string(onePixelPNG) {
		t.Fatalf("unexpected attachment: %#v", attachments)
	}
}

func TestModelImageLimitHonorsCapabilitiesAndMLlama(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte(`{"models":[
          {"name":"vision","modified_at":"2026-01-01T00:00:00Z","capabilities":["completion","vision"],"details":{"family":"qwen3vl"}},
          {"name":"mllama","modified_at":"2026-01-01T00:00:00Z","capabilities":["completion","vision"],"details":{"family":"mllama"}},
          {"name":"text","modified_at":"2026-01-01T00:00:00Z","capabilities":["completion"]}
        ]}`))
	}))
	defer server.Close()
	api := &Server{ollama: ollama.NewClient(server.URL)}

	for model, expected := range map[string]int{"vision": 4, "mllama": 1, "text": 0} {
		limit, err := api.modelImageLimit(context.Background(), model)
		if err != nil || limit != expected {
			t.Fatalf("model %s: got limit %d, error %v; want %d", model, limit, err, expected)
		}
	}
}

func TestDecodeImagesRejectsSpoofedAndExcessImages(t *testing.T) {
	_, err := decodeImages([]imageInput{{Name: "fake.png", MediaType: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("not an image"))}})
	if err == nil || !strings.Contains(err.Error(), "JPEG, PNG, or WebP") {
		t.Fatalf("expected format rejection, got %v", err)
	}
	inputs := make([]imageInput, maxImagesPerMessage+1)
	if _, err := decodeImages(inputs); err == nil {
		t.Fatal("expected image count rejection")
	}
}
