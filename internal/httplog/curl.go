package httplog

import (
	"log"
	"net/http"
	"sort"
	"strings"
)

// LogCurl prints an executable curl command for req.
func LogCurl(prefix string, req *http.Request, body []byte) {
	if req == nil {
		return
	}
	log.Printf("%s: %s", prefix, CurlCommand(req, body))
}

func CurlCommand(req *http.Request, body []byte) string {
	var b strings.Builder
	b.WriteString("curl")
	if req.Method != "" {
		b.WriteString(" -X ")
		b.WriteString(shellQuote(req.Method))
	}

	headerNames := make([]string, 0, len(req.Header))
	for name := range req.Header {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)
	for _, name := range headerNames {
		values := append([]string(nil), req.Header.Values(name)...)
		sort.Strings(values)
		for _, value := range values {
			b.WriteString(" -H ")
			b.WriteString(shellQuote(name + ": " + value))
		}
	}

	if len(body) > 0 {
		b.WriteString(" --data-raw ")
		b.WriteString(shellQuote(string(body)))
	}

	b.WriteString(" ")
	b.WriteString(shellQuote(req.URL.String()))
	return b.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
