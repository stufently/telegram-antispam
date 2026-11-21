package telegram

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestFetchFileReadsUpToTheLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "0123456789")
	}))
	defer srv.Close()
	if b, err := fetchFile(context.Background(), srv.Client(), srv.URL+"/f", 10); err != nil || string(b) != "0123456789" {
		t.Fatalf("exact limit: %q %v", b, err)
	}
	if _, err := fetchFile(context.Background(), srv.Client(), srv.URL+"/f", 9); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("over limit: %v", err)
	}
	if _, err := fetchFile(context.Background(), srv.Client(), srv.URL+"/missing", 1000); err == nil {
		t.Fatal("404 accepted")
	}
}

// The file link embeds the bot token, and a transport error quotes the URL.
func TestFetchFileErrorsDoNotQuoteTheLink(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL + "/file/bot123:SECRET/photos/x.jpg"
	srv.Close() // connection refused from now on
	_, err := fetchFile(context.Background(), http.DefaultClient, url, 10)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("err = %v", err)
	}
}

func TestLargestPhotoFileIDComparesArea(t *testing.T) {
	sizes := []models.PhotoSize{
		{FileID: "mid", Width: 320, Height: 240},
		{FileID: "big", Width: 1280, Height: 960},
		{FileID: "small", Width: 90, Height: 67},
	}
	if got := largestPhotoFileID(sizes); got != "big" {
		t.Fatalf("got %q", got)
	}
	if got := largestPhotoFileID(nil); got != "" {
		t.Fatalf("no photo: %q", got)
	}
	m := &models.Message{ID: 1, Chat: models.Chat{ID: -100}, Photo: sizes}
	if got := ToDomainMessage(m).PhotoFileID; got != "big" {
		t.Fatalf("adapter: %q", got)
	}
}

func TestDownloadClientDoesNotFollowRedirects(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("redirect followed: the token-bearing URL would leak in Referer")
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/x", http.StatusFound)
	}))
	defer srv.Close()
	if _, err := fetchFile(context.Background(), downloadClient, srv.URL+"/file/botSECRET/x", 10); err == nil {
		t.Fatal("redirect accepted as a download")
	}
}
