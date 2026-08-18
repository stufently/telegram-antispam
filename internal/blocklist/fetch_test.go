package blocklist

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestParseIDsTolerant(t *testing.T) {
	got, err := ParseIDs(strings.NewReader("1\n2\n\n  3 \nnotanid\n4\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{1, 2, 3, 4}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestFetchIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("5\n6\n7\n"))
	}))
	defer srv.Close()

	got, err := FetchIDs(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{5, 6, 7}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestFetchIDsStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	got, err := FetchIDs(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty/nil ids, got %v", got)
	}
}

func TestFetchIDsContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("1\n2\n"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := FetchIDs(ctx, srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected error for canceled context, got nil")
	}
}
