package front

import (
	ctls "crypto/tls"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Config describes the listeners and limits of the public front server. The
// variable names are the ones Thruster reads (each also accepted with a THRUSTER_
// prefix, which wins), so existing deployments keep working.
type Config struct {
	Shards                                 int // gina shard threads that run HTTP; one more hosts the realtime bus
	Pin                                    bool
	CompressionLevel                       int
	GzipCacheBytes                         int64 // memory for memoised compressed bodies (0 disables)
	TargetBind                             string
	TargetPort, HTTPPort, HTTPSPort        int // 0 binds an ephemeral port (tests)
	MaxRequestBody, MaxUpload              int64
	Gzip, DisableGzipOnAuth, H2C           bool
	ForwardHeaders, LogRequests            bool
	Domains                                []string
	ACMEDirectory, StoragePath             string
	EABKeyID, EABKey                       string
	CertFile, KeyFile                      string
	Certificates                           []ctls.Certificate // static certificates given in code (tests)
	TempDir                                string
	IdleTimeout, ReadTimeout, WriteTimeout time.Duration
	DisableTarget                          bool // do not open the internal application listener
	Secure                                 bool // the app is served over TLS (used for redirects and HSTS-free cookies)
	acmeRenewEvery                         time.Duration
}

func FromEnv() Config { return FromLookup(os.LookupEnv) }

func FromLookup(lookup func(string) (string, bool)) Config {
	get := func(key, fallback string) string {
		if value, ok := lookup("THRUSTER_" + key); ok {
			return value
		}
		if value, ok := lookup(key); ok {
			return value
		}
		return fallback
	}
	number := func(key string, fallback int64) int64 {
		n, err := strconv.ParseInt(get(key, ""), 10, 64)
		if err != nil {
			return fallback
		}
		return n
	}
	boolean := func(key string, fallback bool) bool {
		b, err := strconv.ParseBool(get(key, ""))
		if err != nil {
			return fallback
		}
		return b
	}
	port := func(key string, fallback int64) int {
		n := number(key, fallback)
		if n < 0 || n > 65535 {
			n = fallback
		}
		return int(n)
	}
	seconds := func(key string, fallback int64) time.Duration {
		return time.Duration(max(0, number(key, fallback))) * time.Second
	}
	c := Config{
		Shards:            int(max(1, number("SHARDS", int64(min(runtime.NumCPU(), 4))))),
		Pin:               boolean("PIN_SHARDS", false),
		CompressionLevel:  int(number("GZIP_COMPRESSION_LEVEL", 5)),
		GzipCacheBytes:    number("GZIP_CACHE_SIZE", 32<<20),
		TargetBind:        get("TARGET_BIND", "127.0.0.1"),
		TargetPort:        port("TARGET_PORT", 3000),
		HTTPPort:          port("HTTP_PORT", 80),
		HTTPSPort:         port("HTTPS_PORT", 443),
		MaxRequestBody:    number("MAX_REQUEST_BODY", 0),
		MaxUpload:         number("CAMPFIRE_MAX_UPLOAD_BYTES", 10<<30),
		Gzip:              boolean("GZIP_COMPRESSION_ENABLED", true),
		DisableGzipOnAuth: boolean("GZIP_COMPRESSION_DISABLE_ON_AUTH", false),
		H2C:               boolean("H2C_ENABLED", false),
		LogRequests:       boolean("LOG_REQUESTS", true),
		ACMEDirectory:     get("ACME_DIRECTORY", "https://acme-v02.api.letsencrypt.org/directory"),
		StoragePath:       get("STORAGE_PATH", "./storage/thruster"),
		EABKeyID:          get("EAB_KID", ""),
		EABKey:            get("EAB_HMAC_KEY", ""),
		CertFile:          get("TLS_CERT_FILE", ""),
		KeyFile:           get("TLS_KEY_FILE", ""),
		IdleTimeout:       seconds("HTTP_IDLE_TIMEOUT", 60),
		ReadTimeout:       seconds("HTTP_READ_TIMEOUT", 30),
		WriteTimeout:      seconds("HTTP_WRITE_TIMEOUT", 30),
	}
	if net.ParseIP(c.TargetBind) == nil {
		c.TargetBind = "127.0.0.1"
	}
	for _, domain := range strings.Split(get("TLS_DOMAIN", ""), ",") {
		if domain = strings.TrimSpace(domain); domain != "" {
			c.Domains = append(c.Domains, domain)
		}
	}
	c.ForwardHeaders = boolean("FORWARD_HEADERS", len(c.Domains) == 0 && c.CertFile == "")
	return c
}

// tlsEnabled reports whether the public listener speaks TLS.
func (c Config) tlsEnabled() bool {
	return len(c.Domains) > 0 || c.CertFile != "" || len(c.Certificates) > 0
}
