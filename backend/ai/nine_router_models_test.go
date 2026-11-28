package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestFetchNineRouterModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer saved-key" {
			t.Error("unexpected catalog request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, `{"data":[
			{"id":"cx/gpt-5.6-luna","capabilities":{"vision":true}},
			{"id":"ag/gemini-4-neon","capabilities":{"vision":true}},
			{"id":"cc/claude-sonnet-4-6"},
			{"id":"fast-combo"},
			{"id":""},{"id":"cx/gpt-5.6-luna"}
		]}`)
	}))
	defer server.Close()

	got, err := FetchNineRouterModels(context.Background(), server.URL+"/v1/", "saved-key")
	want := []NineRouterModel{
		{ID: "cx/gpt-5.6-luna", Vision: true},
		{ID: "ag/gemini-4-neon", Vision: true},
		{ID: "cc/claude-sonnet-4-6"},
		{ID: "fast-combo"},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog = %+v, %v, want %+v", got, err, want)
	}
}

func TestFetchNineRouterModelsDoesNotFollowRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Error("catalog request followed an upstream redirect")
			return
		}
		http.Redirect(w, r, "/other", http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	if _, err := FetchNineRouterModels(context.Background(), server.URL+"/v1", "saved-key"); err == nil {
		t.Fatal("expected the redirect to be reported as a catalog error")
	}
}
