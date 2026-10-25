package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMemory(t *testing.T) {
	a := &api{ctx: context.Background()}
	handler := a.routes()
	for _, value := range []string{"", "0", "-1", "abc", "1.5", "18446744073709551615"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/eat?mb="+value, nil))
		if w.Code != 400 {
			t.Fatalf("mb=%q: status=%d", value, w.Code)
		}
	}
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/eat?mb=1", nil))
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	if a.heldMiB != 2 || len(a.memory) != 2 || len(a.memory[0]) != 1<<20 || a.memory[0][0] != 1 {
		t.Fatal("memory was not allocated and retained")
	}
}

func TestBurnKeepsHTTPAvailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &api{ctx: ctx}
	handler := a.routes()
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/burn", nil))
		if w.Code != 200 || (i == 1 && !strings.Contains(w.Body.String(), "already running")) {
			t.Fatal(w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
	if w.Code != 200 || w.Body.String() != "ok\n" {
		t.Fatal("health unavailable while burning")
	}
	cancel()
	deadline := time.Now().Add(time.Second)
	for a.burning.Load() {
		if time.Now().After(deadline) {
			t.Fatal("burner did not stop")
		}
		time.Sleep(time.Millisecond)
	}
}
