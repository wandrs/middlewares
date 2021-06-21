package logr

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-logr/logr"
	"github.com/oschwald/geoip2-golang"
)

// Logger is a simple, but powerful implementation of a custom structured
// logger backed on github.com/go-logr/logr.
// Adapted from go-chi's custom logging middleware example, source:
// https://github.com/go-chi/chi/blob/v5.0.3/_examples/logging/main.go

func NewLogger(log logr.Logger, geodb *geoip2.Reader) func(next http.Handler) http.Handler {
	return middleware.RequestLogger(&Logger{log: log, geodb: geodb})
}

type Logger struct {
	log   logr.Logger
	geodb *geoip2.Reader
}

func (l *Logger) NewLogEntry(r *http.Request) middleware.LogEntry {
	kvs := make([]interface{}, 0, 11<<1)

	kvs = append(kvs, "ts", time.Now().UTC().Format(time.RFC3339))

	if reqID := middleware.GetReqID(r.Context()); reqID != "" {
		kvs = append(kvs, "req_id", reqID)
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	kvs = append(kvs, "http_scheme", scheme)
	kvs = append(kvs, "http_proto", r.Proto)
	kvs = append(kvs, "http_method", r.Method)

	kvs = appendGeoData(kvs, r, l.geodb)
	kvs = append(kvs, "user_agent", r.UserAgent())

	kvs = append(kvs, "uri", fmt.Sprintf("%s://%s%s", scheme, r.Host, r.RequestURI))

	entry := &LogrEntry{log: l.log.WithValues(kvs...)}
	entry.log.Info("request started")
	return entry
}

// getIP gets a requests IP address by reading off the forwarded-for
// header (for proxies) and falls back to use the remote address.
func getIP(r *http.Request) string {
	forwarded := r.Header.Get("X-FORWARDED-FOR")
	if forwarded != "" {
		return forwarded
	}
	return r.RemoteAddr
}

func appendGeoData(kvs []interface{}, r *http.Request, db *geoip2.Reader) []interface{} {
	addr := getIP(r)
	kvs = append(kvs, "remote_addr", addr)

	if db == nil {
		return kvs
	}
	ips := strings.Split(addr, ",")
	if len(ips) == 0 {
		return kvs
	}
	var ip net.IP
	if host, _, err := net.SplitHostPort(strings.TrimSpace(ips[0])); err == nil {
		ip = net.ParseIP(host)
	} else {
		ip = net.ParseIP(strings.TrimSpace(ips[0]))
	}
	if ip == nil {
		return kvs
	}
	record, err := db.City(ip)
	if err != nil {
		return kvs
	}

	kvs = append(kvs, "remote_city", record.City.Names["en"])
	kvs = append(kvs, "remote_country", record.Country.IsoCode)
	kvs = append(kvs, "remote_tz", record.Location.TimeZone)

	return kvs
}

type LogrEntry struct {
	log logr.Logger
}

func (l *LogrEntry) Write(status, bytes int, header http.Header, elapsed time.Duration, extra interface{}) {
	l.log = l.log.WithValues(
		"resp_status", status,
		"resp_bytes_length", bytes,
		"resp_elapsed_ms", float64(elapsed.Nanoseconds())/1000000.0,
	)

	l.log.Info("request complete")
}

func (l *LogrEntry) Panic(v interface{}, stack []byte) {
	l.log = l.log.WithValues(
		"stack", string(stack),
		"panic", fmt.Sprintf("%+v", v),
	)
}

// Helper methods used by the application to get the request-scoped
// logger entry and set additional fields between handlers.
//
// This is a useful pattern to use to set state on the entry as it
// passes through the handler chain, which at any point can be logged
// with a call to .Print(), .Info(), etc.

func GetLogEntry(r *http.Request) logr.Logger {
	entry := middleware.GetLogEntry(r).(*LogrEntry)
	return entry.log
}

func LogEntrySetField(r *http.Request, key string, value interface{}) {
	if entry, ok := r.Context().Value(middleware.LogEntryCtxKey).(*LogrEntry); ok {
		entry.log = entry.log.WithValues(key, value)
	}
}

func LogEntrySetFields(r *http.Request, keysAndValues ...interface{}) {
	if entry, ok := r.Context().Value(middleware.LogEntryCtxKey).(*LogrEntry); ok {
		entry.log = entry.log.WithValues(keysAndValues...)
	}
}
