package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/majd/ipatool/v2/pkg/appstore"
	apphttp "github.com/majd/ipatool/v2/pkg/http"
	"github.com/majd/ipatool/v2/pkg/util/operatingsystem"
)

func TestAuthenticationErrors(t *testing.T) {
	rejected := appstore.NewErrorWithMetadata(errors.New("localized credential rejection"), apphttp.Result[map[string]string]{
		StatusCode: 200, Data: map[string]string{"FailureType": "-5000"},
	})
	cases := []struct {
		name       string
		err        error
		code, want string
	}{
		{"explicit 2FA", fmt.Errorf("login: %w", appstore.ErrAuthCodeRequired), "", "AUTH_CODE_REQUIRED"},
		{"wrong password", rejected, "", "INVALID_CREDENTIALS"},
		{"password plus code rejected", rejected, "123456", "AUTH_VERIFICATION_FAILED"},
		{"invalid code", errors.New("apple did not complete verification; try a fresh 2FA code"), "123456", "AUTH_CODE_INVALID"},
		{"bad code format", errors.New("2FA code must contain exactly six digits"), "12", "AUTH_CODE_INVALID"},
		{"disabled account", appstore.NewErrorWithMetadata(errors.New("account is disabled"), nil), "", "ACCOUNT_DISABLED"},
		{"no-content server response", &apphttp.UnexpectedResponseError{StatusCode: 204}, "", "AUTH_SERVICE_ERROR"},
		{"transport failure during 2FA", &apphttp.TransportError{Err: errors.New("connection reset")}, "123456", "AUTH_SERVICE_ERROR"},
		{"unknown protocol failure", errors.New("apple requested an authentication machine action: SP"), "", "AUTH_FAILED"},
		{"unknown error after code", errors.New("unable to initialize SAP"), "123456", "AUTH_FAILED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := loginErrorCode(tc.err, tc.code); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestDeviceStateSurvivesHelpersWithoutPersistingPassword(t *testing.T) {
	directory := t.TempDir()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	open := func() *authenticationKeychain {
		t.Helper()
		k, err := newAuthenticationKeychain(directory, "01:02:03:04:05:06", key)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	first := open()
	if err := first.Set(anisetteStateKey, []byte("provisioned-before-2FA")); err != nil {
		t.Fatal(err)
	}
	if err := first.Set("account", []byte("account-password")); err != nil {
		t.Fatal(err)
	}
	challengeJar, _ := newMemoryJar(nil)
	origin, _ := url.Parse("https://auth.itunes.apple.com/auth/v1/native")
	challengeJar.SetCookies(origin, []*http.Cookie{{Name: "challenge", Value: "pending-2fa", Secure: true}})
	if err := saveAuthenticationCookies(first, "user@example.test", challengeJar); err != nil {
		t.Fatal(err)
	}
	filename := first.path
	first.Close() // First helper exits with a 2FA challenge.
	second := open()
	state, err := second.Get(anisetteStateKey)
	if err != nil || string(state) != "provisioned-before-2FA" {
		t.Fatalf("state lost: %q %v", state, err)
	}
	restored, err := restoreAuthenticationCookies(second, "USER@example.test", nil)
	if err != nil || len(restored.Cookies(origin)) != 1 {
		t.Fatal("2FA challenge cookies lost")
	}
	other, err := restoreAuthenticationCookies(second, "other@example.test", nil)
	if err != nil || len(other.Cookies(origin)) != 0 {
		t.Fatal("cookies leaked to a different account")
	}
	if _, err := second.Get("account"); err == nil {
		t.Fatal("account password persisted")
	}
	if err := second.Set(anisetteStateKey, []byte("ready-for-download")); err != nil {
		t.Fatal(err)
	}
	second.Close()
	third := open()
	state, err = third.Get(anisetteStateKey)
	if err != nil || string(state) != "ready-for-download" {
		t.Fatalf("state lost: %q %v", state, err)
	}
	third.Close()
	raw, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, state) || bytes.Contains(raw, []byte("account-password")) {
		t.Fatal("secrets stored in plaintext")
	}
	info, _ := os.Stat(filename)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("insecure permissions: %v", info.Mode())
	}
	wrong := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	if k, err := newAuthenticationKeychain(directory, "01:02:03:04:05:06", wrong); err == nil {
		k.Close()
		t.Fatal("accepted wrong key")
	}
	// Authentication failure must not destroy the valid state.
	final := open()
	final.Close()
	raw[len(raw)-1] ^= 1
	if err := os.WriteFile(filename, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if k, err := newAuthenticationKeychain(directory, "01:02:03:04:05:06", key); err == nil {
		k.Close()
		t.Fatal("accepted tampered state")
	}
}

func TestConcurrentHelpersWaitForLatestDeviceState(t *testing.T) {
	directory := t.TempDir()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	first, err := newAuthenticationKeychain(directory, "device", key)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	received := make(chan *authenticationKeychain, 1)
	failures := make(chan error, 1)
	go func() {
		next, err := newAuthenticationKeychain(directory, "device", key)
		if err != nil {
			failures <- err
			return
		}
		received <- next
	}()
	select {
	case next := <-received:
		next.Close()
		t.Fatal("second helper bypassed device lock")
	case err := <-failures:
		t.Fatal(err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := first.Set(anisetteStateKey, []byte("latest")); err != nil {
		t.Fatal(err)
	}
	first.Close()
	select {
	case next := <-received:
		defer next.Close()
		state, err := next.Get(anisetteStateKey)
		if err != nil || string(state) != "latest" {
			t.Fatalf("stale state %q: %v", state, err)
		}
	case err := <-failures:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("device lock was not released")
	}
}

func TestCookiesSurviveHelperBoundary(t *testing.T) {
	jar, _ := newMemoryJar(nil)
	origin, _ := url.Parse("https://auth.itunes.apple.com/auth/v1/native")
	jar.SetCookies(origin, []*http.Cookie{
		{Name: "host-session", Value: "secret", Secure: true, HttpOnly: true},
		{Name: "store-session", Value: "token", Domain: ".itunes.apple.com", Path: "/", MaxAge: 60, Secure: true},
	})
	restored, err := newMemoryJar(jar.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Cookies(origin)) != 2 {
		t.Fatal("lost session or default-path cookie")
	}
	pod, _ := url.Parse("https://p7-buy.itunes.apple.com/")
	cookies := restored.Cookies(pod)
	if len(cookies) != 1 || cookies[0].Name != "store-session" {
		t.Fatal("lost domain cookie or leaked host-only cookie")
	}
	jar.SetCookies(origin, []*http.Cookie{{Name: "store-session", Domain: ".itunes.apple.com", Path: "/", MaxAge: -1}})
	restored, _ = newMemoryJar(jar.snapshot())
	if len(restored.Cookies(pod)) != 0 {
		t.Fatal("deleted cookie resurrected")
	}
}

func TestErrorMessagesDoNotExposeCredentials(t *testing.T) {
	input := request{Email: "private@example.test", Password: "secret-password", Code: "123456", SessionKey: "secret-key"}
	message := safeErrorMessage(input, errors.New(input.Email+" "+input.Password+input.Code+" "+input.SessionKey))
	for _, secret := range []string{input.Email, input.Password, input.Code, input.SessionKey} {
		if strings.Contains(message, secret) {
			t.Fatal("secret exposed")
		}
	}
}

func TestTransferReleasesDeviceLockWithoutOverwritingNewState(t *testing.T) {
	directory := t.TempDir()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))
	first, err := newAuthenticationKeychain(directory, "device", key)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.Set(anisetteStateKey, []byte("first-proof")); err != nil {
		t.Fatal(err)
	}
	if err := first.Set("kbsync-v1", []byte("cached-download-proof")); err != nil {
		t.Fatal(err)
	}
	jar, _ := newMemoryJar(nil)
	fileSystem := &transferOperatingSystem{OperatingSystem: operatingsystem.New(), beforeTransfer: func() error {
		if err := saveAuthenticationCookies(first, "test@example.invalid", jar); err != nil {
			return err
		}
		return first.releaseForTransfer()
	}}
	transfer, err := fileSystem.OpenFile(filepath.Join(directory, "download.ipa.tmp"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer transfer.Close() // Still downloading while the second helper starts.
	ready := make(chan *authenticationKeychain, 1)
	failure := make(chan error, 1)
	go func() {
		next, err := newAuthenticationKeychain(directory, "device", key)
		if err != nil {
			failure <- err
			return
		}
		ready <- next
	}()
	select {
	case second := <-ready:
		if state, _ := second.Get("kbsync-v1"); string(state) != "cached-download-proof" {
			t.Error("download proof not cached")
		}
		if err := second.Set(anisetteStateKey, []byte("second-proof")); err != nil {
			t.Error(err)
		}
		second.Close()
	case err := <-failure:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("second task blocked by file transfer")
	}
	// Completion of the older transfer must not restore stale ADI or cookies.
	if err := saveAuthenticationCookies(first, "test@example.invalid", jar); err != nil {
		t.Fatal(err)
	}
	if err := first.Set(anisetteStateKey, []byte("stale")); err == nil {
		t.Fatal("stale device state accepted")
	}
	third, err := newAuthenticationKeychain(directory, "device", key)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	state, _ := third.Get(anisetteStateKey)
	if string(state) != "second-proof" {
		t.Fatalf("state rolled back: %q", state)
	}
}

func TestBusyAuthenticationLockHasDeadline(t *testing.T) {
	directory := t.TempDir()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))
	first, err := newAuthenticationKeychain(directory, "device", key)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	contender, err := os.OpenFile(strings.TrimSuffix(first.path, ".enc")+".lock", os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer contender.Close()
	started := time.Now()
	if err := acquireStateLock(contender, 60*time.Millisecond); !errors.Is(err, errAuthenticationBusy) {
		t.Fatalf("unexpected result: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("lock wait exceeded deadline")
	}
}
