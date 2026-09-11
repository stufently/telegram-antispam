package detect

import (
	"net/url"
	"path"
	"strings"
)

// allowedGoogleMapsURL reports whether link is an exact Google Maps URL.
// Matching uses the original collected bytes (no Deobfuscate/NFKC/ZWSP fold).
// Hostnames are ASCII and compared case-insensitively; the path is
// case-sensitive. Query and fragment are ignored. Redirects are not fetched.
func allowedGoogleMapsURL(link string) bool {
	u, ok := parseMapsURL(link)
	if !ok {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if !asciiHost(host) {
		return false
	}
	decoded := u.Path
	escaped := u.EscapedPath()
	switch host {
	case "maps.app.goo.gl":
		return gooGlShortPath(decoded)
	case "maps.google.com":
		return googleMapsPath(decoded, escaped, true)
	case "google.com", "www.google.com":
		return googleMapsPath(decoded, escaped, false)
	default:
		return false
	}
}

func parseMapsURL(link string) (*url.URL, bool) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, false
	}
	if u.Scheme == "" {
		// Scheme-less Telegram "url" entity ("maps.app.goo.gl/AbCd").
		// Only reparse as authority when Parse did not already find a host;
		// never prepend "//" to a schemed URL.
		if u.Host != "" {
			return nil, false
		}
		u, err = url.Parse("//" + link)
		if err != nil {
			return nil, false
		}
	} else if u.Scheme != "http" && u.Scheme != "https" {
		return nil, false
	}
	if u.Host == "" || u.User != nil || u.Port() != "" {
		return nil, false
	}
	return u, true
}

func asciiHost(host string) bool {
	if host == "" || strings.HasSuffix(host, ".") {
		return false
	}
	for i := 0; i < len(host); i++ {
		if host[i] > 127 {
			return false
		}
	}
	return true
}

func googleMapsPath(decoded, escaped string, allowRoot bool) bool {
	if isRootPath(decoded) {
		return allowRoot && firstEscapedSegment(escaped) == ""
	}
	cleaned := path.Clean(decoded)
	if !(mapsPathBoundary(decoded) && mapsPathBoundary(cleaned)) {
		return false
	}
	return firstEscapedSegment(escaped) == "maps"
}

func mapsPathBoundary(p string) bool {
	return p == "/maps" || strings.HasPrefix(p, "/maps/")
}

func gooGlShortPath(decoded string) bool {
	if isRootPath(decoded) {
		return false
	}
	cleaned := path.Clean(decoded)
	if isRootPath(cleaned) || cleaned == "." {
		return false
	}
	first := firstPathSegment(decoded)
	return first != "" && first != "." && first != ".."
}

func isRootPath(p string) bool {
	return p == "" || p == "/"
}

func firstPathSegment(p string) string {
	p = strings.TrimPrefix(p, "/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return p
}

func firstEscapedSegment(escaped string) string {
	return firstPathSegment(escaped)
}
