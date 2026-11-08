package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	apphttp "github.com/majd/ipatool/v2/pkg/http"
)

// net/http/cookiejar.Cookies returns only request fields (Name/Value), which
// cannot restore domain/path-scoped sessions. Keep each cookie's origin and
// scope alongside the standard jar, including host-only and session cookies.
type storedCookie struct {
	Origin string      `json:"origin"`
	Cookie http.Cookie `json:"cookie"`
}

type memoryJar struct {
	*cookiejar.Jar
	mu    sync.Mutex
	state map[string]storedCookie
}

var _ apphttp.CookieJar = (*memoryJar)(nil)

func newMemoryJar(state []storedCookie) (*memoryJar, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	result := &memoryJar{Jar: jar, state: make(map[string]storedCookie)}
	for _, record := range state {
		origin, err := url.Parse(record.Origin)
		if err != nil || origin.Scheme != "https" || origin.Host == "" {
			continue
		}
		cookie := record.Cookie
		result.SetCookies(origin, []*http.Cookie{&cookie})
	}
	return result, nil
}

func (*memoryJar) Save() error { return nil }

func (j *memoryJar) SetCookies(origin *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Jar.SetCookies(origin, cookies)
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		saved := *cookie
		if saved.Path == "" || !strings.HasPrefix(saved.Path, "/") {
			saved.Path = "/"
			if index := strings.LastIndex(origin.Path, "/"); index > 0 {
				saved.Path = origin.Path[:index]
			}
		}
		domain := strings.ToLower(strings.TrimPrefix(saved.Domain, "."))
		if domain == "" {
			domain = strings.ToLower(origin.Hostname())
		}
		key := domain + "\n" + saved.Path + "\n" + saved.Name
		if saved.MaxAge < 0 || (saved.MaxAge == 0 && !saved.Expires.IsZero() && !saved.Expires.After(time.Now())) {
			delete(j.state, key)
			continue
		}
		if saved.MaxAge > 0 {
			saved.Expires = time.Now().Add(time.Duration(saved.MaxAge) * time.Second)
			saved.MaxAge = 0 // Replaying must not extend the server's lifetime.
		}
		j.state[key] = storedCookie{
			Origin: (&url.URL{Scheme: origin.Scheme, Host: origin.Host, Path: saved.Path}).String(),
			Cookie: saved,
		}
	}
}

func (j *memoryJar) snapshot() []storedCookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	result := make([]storedCookie, 0, len(j.state))
	for _, cookie := range j.state {
		if cookie.Cookie.Expires.IsZero() || cookie.Cookie.Expires.After(time.Now()) {
			result = append(result, cookie)
		}
	}
	return result
}

func authenticationCookieKey(email string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return fmt.Sprintf("cookies/%x", digest)
}

func restoreAuthenticationCookies(keys *authenticationKeychain, email string, fallback []storedCookie) (*memoryJar, error) {
	if data, err := keys.Get(authenticationCookieKey(email)); err == nil {
		// The locked device store is newer than a Node session snapshot. This
		// also restores cookies issued before an earlier 2FA challenge exited.
		if err := json.Unmarshal(data, &fallback); err != nil {
			return nil, fmt.Errorf("decode Apple authentication cookies: %w", err)
		}
	}
	return newMemoryJar(fallback)
}

func saveAuthenticationCookies(keys *authenticationKeychain, email string, jar *memoryJar) error {
	keys.mu.Lock()
	transferring := keys.transferring
	keys.mu.Unlock()
	if transferring {
		return nil
	}
	data, err := json.Marshal(jar.snapshot())
	if err != nil {
		return err
	}
	return keys.Set(authenticationCookieKey(email), data)
}
