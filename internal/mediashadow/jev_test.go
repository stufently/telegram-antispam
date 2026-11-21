package mediashadow

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const okAnswers = `{"answers":{"promo":{"type":"noul","noul":0.97},"contact":{"type":"noul","noul":0.88},"ordinary":{"type":"noul","noul":0.03}}}`

func TestJEVSendsThePictureAndReadsThreeAnswers(t *testing.T) {
	picture := []byte("\xff\xd8 fake jpeg")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("TypeSafe-Danger-Zone-Images") != "true" {
			t.Error("images header missing: the API refuses pictures without it")
		}
		if r.Header.Get("Authorization") != "Bearer k-1" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		var body struct {
			Model     string                       `json:"model"`
			State     map[string]string            `json:"state"`
			Assets    map[string]map[string]string `json:"assets"`
			Questions map[string]map[string]any    `json:"questions"`
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		ref := body.State["photo"]
		a := body.Assets[ref]
		if ref == "" || len(body.Assets) != 1 || a["type"] != "image" || a["format"] != "jpeg" ||
			a["data_base64"] != base64.StdEncoding.EncodeToString(picture) {
			t.Errorf("asset/state wrong: state=%v assets=%v", body.State, body.Assets)
		}
		// The reference must appear exactly once (each mention is billed),
		// and the questions point at the state path, not at the reference.
		if n := strings.Count(string(raw), ref); n != 2 {
			t.Errorf("reference appears %d times, want 2 (state value + asset key)", n)
		}
		if body.Model != "jev-latest" {
			t.Errorf("model = %q", body.Model)
		}
		for _, q := range []string{"promo", "contact", "ordinary"} {
			if body.Questions[q]["type"] != "noul" || !strings.Contains(body.Questions[q]["instructions"].(string), "`photo`") {
				t.Errorf("question %s = %v", q, body.Questions[q])
			}
		}
		_, _ = io.WriteString(w, okAnswers)
	}))
	defer srv.Close()
	s, err := JEV{APIKey: "k-1", Endpoint: srv.URL}.Classify(context.Background(), picture, "jpeg")
	if err != nil || s != (Scores{Promo: .97, Contact: .88, Ordinary: .03}) {
		t.Fatalf("scores=%+v err=%v", s, err)
	}
}

func TestJEVFailuresAreErrorsWithoutTheKeyOrBody(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"400":            {400, `{"error":"Invalid image dimensions k-secret"}`},
		"missing answer": {200, `{"answers":{"promo":{"type":"noul","noul":0.9},"contact":{"type":"noul","noul":0.1}}}`},
		"wrong type":     {200, strings.Replace(okAnswers, `"ordinary":{"type":"noul"`, `"ordinary":{"type":"choice"`, 1)},
		"out of range":   {200, strings.Replace(okAnswers, "0.97", "1.5", 1)},
		"negative":       {200, strings.Replace(okAnswers, "0.03", "-0.01", 1)},
		"null":           {200, strings.Replace(okAnswers, "0.88", "null", 1)},
		"not json":       {200, `<html>k-secret</html>`},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			_, err := JEV{APIKey: "k-secret", Endpoint: srv.URL}.Classify(context.Background(), []byte("x"), "jpeg")
			if err == nil {
				t.Fatal("accepted")
			}
			if strings.Contains(err.Error(), "k-secret") {
				t.Fatalf("error leaks key or body: %v", err)
			}
		})
	}
}

func TestJEVWithoutKeyMakesNoRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("request sent without a key")
	}))
	defer srv.Close()
	if _, err := (JEV{Endpoint: srv.URL}).Classify(context.Background(), []byte("x"), "jpeg"); err == nil {
		t.Fatal("no error without a key")
	}
}

func TestJEVHonoursTheDeadline(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release) // runs first: the handler must return before Close waits for it
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := (JEV{APIKey: "k", Endpoint: srv.URL}).Classify(ctx, []byte("x"), "jpeg"); err == nil {
		t.Fatal("no error after the deadline")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("deadline ignored")
	}
}
