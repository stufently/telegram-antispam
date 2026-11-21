package mediashadow

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
)

// DefaultEndpoint is TypeSafe's System One API.
const DefaultEndpoint = "https://api.typesafe.ai/v1/systemone"

// photoRef names the picture inside the request. The API substitutes the
// picture wherever the reference appears, and a reference that appears
// nowhere is a 400, so it is used exactly once: as state.photo.
const photoRef = "photo-7d1c2a9e"

// Scores are the three probabilities the shadow puts on the card.
type Scores struct {
	Promo, Contact, Ordinary float64
}

// The questions. Promo and Contact are the signal; Ordinary is the paired
// control asked about the same picture from the other side, so a picture the
// model cannot read shows up as two middling answers rather than as one
// confident-looking number.
var questions = map[string]any{
	"promo": map[string]any{
		"type": "noul",
		"instructions": "Is `photo` an advertisement or a promotional offer: goods or services for sale, " +
			"a job or easy earnings, investment, crypto, casino or betting, adult services?",
		"criteria": map[string]string{
			"true":  "A flyer, banner, price list, offer or recruitment post meant to attract customers or workers",
			"false": "An ordinary picture shared in conversation: a personal photo, a meme, a place, a pet, a screenshot of a chat or a map",
		},
	},
	"contact": map[string]any{
		"type": "noul",
		"instructions": "Does `photo` show a way to contact someone or go somewhere: a @username, " +
			"a phone number, a website or link, a messenger handle, or a QR code?",
	},
	"ordinary": map[string]any{
		"type": "noul",
		"instructions": "Is `photo` an ordinary picture shared in conversation, with no offer, " +
			"no advertising and no call to contact or buy?",
	},
}

// JEV asks TypeSafe's System One about one picture. Images are a
// "dreamer-ready" feature on TypeSafe's side (format and accuracy may change
// between releases), which is why the answers only ever label a card.
type JEV struct {
	APIKey   string
	Model    string // empty means jev-latest
	Endpoint string // empty means DefaultEndpoint
	Client   *http.Client
}

// Classify sends a prepared JPEG or PNG and returns the three probabilities.
// Errors never include the response body or the transport error: either may
// echo the request, which carries the key.
func (j JEV) Classify(ctx context.Context, picture []byte, format string) (Scores, error) {
	if j.APIKey == "" {
		return Scores{}, errors.New("jev: no API key")
	}
	model := j.Model
	if model == "" {
		model = "jev-latest"
	}
	endpoint := j.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	body, err := json.Marshal(map[string]any{
		"model":     model,
		"state":     map[string]string{"photo": photoRef},
		"assets":    map[string]any{photoRef: map[string]string{"type": "image", "format": format, "data_base64": base64.StdEncoding.EncodeToString(picture)}},
		"questions": questions,
	})
	if err != nil {
		return Scores{}, errors.New("jev: cannot encode request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Scores{}, errors.New("jev: invalid endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+j.APIKey)
	req.Header.Set("Content-Type", "application/json")
	// Without it the API refuses any request that carries a picture.
	req.Header.Set("TypeSafe-Danger-Zone-Images", "true")
	client := j.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Scores{}, fmt.Errorf("jev: %w", ctx.Err())
		}
		return Scores{}, errors.New("jev: request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Scores{}, fmt.Errorf("jev: status %d", resp.StatusCode)
	}
	const maxResponse = 64 * 1024
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(data) > maxResponse {
		return Scores{}, errors.New("jev: unreadable response")
	}
	var out struct {
		Answers map[string]struct {
			Type string   `json:"type"`
			Noul *float64 `json:"noul"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return Scores{}, errors.New("jev: invalid JSON response")
	}
	var s Scores
	for name, dst := range map[string]*float64{"promo": &s.Promo, "contact": &s.Contact, "ordinary": &s.Ordinary} {
		a, ok := out.Answers[name]
		if !ok || a.Type != "noul" || a.Noul == nil || math.IsNaN(*a.Noul) || *a.Noul < 0 || *a.Noul > 1 {
			return Scores{}, fmt.Errorf("jev: missing or invalid %s answer", name)
		}
		*dst = *a.Noul
	}
	return s, nil
}
