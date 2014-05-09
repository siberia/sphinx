package handlers

import (
	"bytes"
	"code.google.com/p/go-uuid/uuid"
	"github.com/Clever/leakybucket"
	"github.com/Clever/sphinx"
	"github.com/Clever/sphinx/common"
	"log"
	"net/http"
	"strconv"
	"strings"
)

func stringifyHeaders(headers http.Header) *bytes.Buffer {
	buf := &bytes.Buffer{}
	for header, values := range headers {
		buf.WriteString(header)
		buf.WriteString("=")
		buf.WriteString(strings.Join(values, ","))
		buf.WriteString(" ")
	}
	return buf
}
func stringifyRequest(req common.Request) *bytes.Buffer {
	buf := &bytes.Buffer{}
	for _, field := range []string{"path", "remoteaddr"} {
		buf.WriteString(field)
		buf.WriteString("=")
		buf.WriteString(req[field].(string))
		buf.WriteString(" ")
	}
	buf.Write(stringifyHeaders(req["headers"].(http.Header)).Bytes())
	return buf
}

type httpRateLimiter struct {
	rateLimiter sphinx.RateLimiter
	proxy       http.Handler
}

func (hrl httpRateLimiter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	guid := uuid.New()
	request := common.HTTPToSphinxRequest(r)
	log.Printf("[%s] REQUEST: %s", guid, stringifyRequest(request).String())
	matches, err := hrl.rateLimiter.Add(request)
	if err != nil && err != leakybucket.ErrorFull {
		log.Printf("[%s] ERROR: %s", guid, err)
		w.WriteHeader(500)
		return
	}
	addRateLimitHeaders(w, matches)
	if err == leakybucket.ErrorFull {
		w.WriteHeader(429)
		return
	}
	hrl.proxy.ServeHTTP(w, r)
}

type httpRateLogger httpRateLimiter

func (hrl httpRateLogger) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	guid := uuid.New()
	request := common.HTTPToSphinxRequest(r)
	log.Printf("[%s] REQUEST: %s", guid, stringifyRequest(request).String())
	matches, err := hrl.rateLimiter.Add(request)
	if err != nil && err != leakybucket.ErrorFull {
		log.Printf("[%s] ERROR: %s", guid, err)
		hrl.proxy.ServeHTTP(w, r)
		return
	}
	log.Printf("[%s] RATE LIMIT HEADERS: %s", guid, stringifyHeaders(getRateLimitHeaders(matches)).String())
	if err == leakybucket.ErrorFull {
		log.Printf("[%s] BUCKET FULL", guid)
	}
	hrl.proxy.ServeHTTP(w, r)
}

func uintToString(num uint) string {
	return strconv.Itoa(int(num))
}

func int64ToString(num int64) string {
	return strconv.Itoa(int(num))
}

func initHeaders() map[string][]string {
	headers := map[string][]string{}
	for _, header := range []string{"Limit", "Reset", "Remaining", "Bucket"} {
		headerName := "X-Ratelimit-" + header
		if headers[headerName] == nil {
			headers[headerName] = []string{}
		}
	}
	return headers
}

func getRateLimitHeaders(statuses []sphinx.Status) map[string][]string {
	if len(statuses) == 0 {
		return map[string][]string{}
	}
	headers := initHeaders()
	for _, status := range statuses {
		headers["X-Ratelimit-Limit"] = append(headers["X-Ratelimit-Limit"], uintToString(status.Capacity))
		headers["X-Ratelimit-Reset"] = append(headers["X-Ratelimit-Reset"], int64ToString(status.Reset.Unix()))
		headers["X-Ratelimit-Remaining"] = append(headers["X-Ratelimit-Remaining"], uintToString(status.Remaining))
		headers["X-Ratelimit-Bucket"] = append(headers["X-Ratelimit-Bucket"], status.Name)
	}
	return headers
}

func addRateLimitHeaders(w http.ResponseWriter, statuses []sphinx.Status) {
	for header, values := range getRateLimitHeaders(statuses) {
		for _, value := range values {
			w.Header().Add(header, value)
		}
	}
}

// NewHTTPLimiter returns an http.Handler that rate limits and proxies requests.
func NewHTTPLimiter(rateLimiter sphinx.RateLimiter, proxy http.Handler) http.Handler {
	return httpRateLimiter{rateLimiter: rateLimiter, proxy: proxy}
}

// NewHTTPLogger returns an http.Handler that logs the results of rate limiting requests, but
// actually proxies everything.
func NewHTTPLogger(rateLimiter sphinx.RateLimiter, proxy http.Handler) http.Handler {
	return httpRateLogger{rateLimiter: rateLimiter, proxy: proxy}
}
