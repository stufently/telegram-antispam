package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	bot "github.com/go-telegram/bot"
)

// ErrFileTooLarge means the file is bigger than the caller agreed to read.
var ErrFileTooLarge = errors.New("telegram: file larger than the limit")

// DownloadFile resolves fileID with getFile and reads at most maxBytes of the
// file. The getFile call is a Bot API method and goes through the dispatcher
// like every other one; the download itself is a plain GET to Telegram's file
// server, outside the Bot API rate limits, so it runs here and does not hold
// a dispatcher slot for the length of a transfer.
//
// Errors never carry the download URL: it embeds the bot token.
func (p *LivePort) DownloadFile(ctx context.Context, fileID string, maxBytes int64) ([]byte, error) {
	if fileID == "" || maxBytes <= 0 {
		return nil, errors.New("telegram: download: empty file id or limit")
	}
	fl, err := submitSync(ctx, p.disp, 0, p.prio("DownloadFile"), func(ctx context.Context) (fileLink, error) {
		f, err := p.b.GetFile(ctx, &bot.GetFileParams{FileID: fileID})
		if err != nil {
			return fileLink{}, markRefused(mapRetry(err))
		}
		return fileLink{url: p.b.FileDownloadLink(f), size: f.FileSize}, nil
	})
	if err != nil {
		return nil, fmt.Errorf("telegram: getFile: %w", err)
	}
	if fl.size > maxBytes {
		return nil, ErrFileTooLarge
	}
	return fetchFile(ctx, http.DefaultClient, fl.url, maxBytes)
}

type fileLink struct {
	url  string
	size int64
}

// fetchFile GETs url and returns at most maxBytes of its body.
func fetchFile(ctx context.Context, client *http.Client, url string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errors.New("telegram: download: bad file link")
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("telegram: download: %w", ctx.Err())
		}
		// The transport error quotes the URL, and the URL quotes the token.
		return nil, errors.New("telegram: download failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram: download: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, errors.New("telegram: download: read failed")
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrFileTooLarge
	}
	return data, nil
}
