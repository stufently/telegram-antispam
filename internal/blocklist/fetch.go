package blocklist

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// ParseIDs scans r line by line, trimming whitespace and parsing each line
// as a base-10 int64. Blank lines and lines that fail to parse are skipped
// (tolerant: a stray header/comment/BOM must not fail the whole parse). The
// returned error is non-nil only for a genuine scanner read error, never for
// a per-line parse failure.
func ParseIDs(r io.Reader) ([]int64, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var ids []int64
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		id, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

// FetchIDs GETs url with the given context and client, and parses the
// response body as a newline-delimited list of int64 ids. The caller
// supplies the *http.Client so the timeout is injectable/testable.
func FetchIDs(ctx context.Context, client *http.Client, url string) ([]int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("blocklist fetch %s: %w", url, err)
	}
	// Set an explicit User-Agent: some CDNs in front of the blocklist hosts
	// reject the default Go-http-client UA (which would come back as a non-2xx
	// error, or worse a 200 challenge page that parses to zero ids).
	req.Header.Set("User-Agent", "telegram-antispam-blocklist/1.0 (+https://github.com/stufently/telegram-antispam)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("blocklist fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("blocklist fetch %s: status %d", url, resp.StatusCode)
	}

	return ParseIDs(resp.Body)
}
