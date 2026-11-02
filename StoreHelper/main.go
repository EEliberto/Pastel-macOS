package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/majd/ipatool/v2/pkg/appstore"
	apphttp "github.com/majd/ipatool/v2/pkg/http"
	"github.com/majd/ipatool/v2/pkg/util/operatingsystem"
	"github.com/schollz/progressbar/v3"
)

type request struct {
	Command    string           `json:"command"`
	Email      string           `json:"email,omitempty"`
	Password   string           `json:"password,omitempty"`
	Code       string           `json:"code,omitempty"`
	GUID       string           `json:"guid"`
	Account    appstore.Account `json:"account,omitempty"`
	Cookies    []*http.Cookie   `json:"cookies,omitempty"`
	App        appstore.App     `json:"app,omitempty"`
	Platform   string           `json:"platform,omitempty"`
	VersionID  string           `json:"versionId,omitempty"`
	OutputPath string           `json:"outputPath,omitempty"`
}

type response struct {
	OK       bool                         `json:"ok"`
	Code     string                       `json:"code,omitempty"`
	Message  string                       `json:"message,omitempty"`
	Account  *appstore.Account            `json:"account,omitempty"`
	Cookies  []*http.Cookie               `json:"cookies,omitempty"`
	Versions *appstore.ListVersionsOutput `json:"versions,omitempty"`
	Download *appstore.DownloadOutput     `json:"download,omitempty"`
}

type memoryKeychain struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (k *memoryKeychain) Get(key string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	value, ok := k.values[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), value...), nil
}

func (k *memoryKeychain) Set(key string, value []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.values[key] = append([]byte(nil), value...)
	return nil
}

func (k *memoryKeychain) Remove(key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.values, key)
	return nil
}

type memoryJar struct{ *cookiejar.Jar }

func (memoryJar) Save() error { return nil }

var _ apphttp.CookieJar = memoryJar{}

type fixedMachine struct{ mac string }

func (m fixedMachine) MacAddress() (string, error) { return m.mac, nil }
func (fixedMachine) HomeDirectory() string         { return os.TempDir() }
func (fixedMachine) ReadPassword(int) ([]byte, error) {
	return nil, errors.New("interactive password input is disabled")
}

func main() {
	var input request
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		fail(input, "INVALID_INPUT", err)
	}

	mac, err := macAddress(input.GUID)
	if err != nil {
		fail(input, "INVALID_DEVICE", err)
	}

	baseJar, err := cookiejar.New(nil)
	if err != nil {
		fail(input, "INTERNAL", err)
	}
	jar := memoryJar{baseJar}
	cookieURLs := restoreCookies(jar, input.Cookies)
	keys := &memoryKeychain{values: make(map[string][]byte)}
	if input.Account.PasswordToken != "" && input.Account.DirectoryServicesID != "" {
		encoded, encodeErr := json.Marshal(input.Account)
		if encodeErr != nil {
			fail(input, "INVALID_SESSION", encodeErr)
		}
		keys.values["account"] = encoded
	}

	store := appstore.NewAppStore(appstore.Args{
		Keychain:        keys,
		CookieJar:       jar,
		OperatingSystem: operatingsystem.New(),
		Machine:         fixedMachine{mac: mac},
	})

	account := input.Account
	result := response{OK: true}
	if input.Command != "login" {
		login, loginErr := store.Login(appstore.LoginInput{
			Email: input.Email, Password: input.Password, AuthCode: input.Code,
		})
		if loginErr != nil {
			fail(input, errorCode(loginErr, true), loginErr)
		}
		account = login.Account
	}
	switch input.Command {
	case "login":
		login, loginErr := store.Login(appstore.LoginInput{
			Email: input.Email, Password: input.Password, AuthCode: input.Code,
		})
		if loginErr != nil {
			fail(input, errorCode(loginErr, true), loginErr)
		}
		account = login.Account
	case "listVersions":
		platform, platformErr := appstore.ParsePlatform(input.Platform)
		if platformErr != nil {
			fail(input, "INVALID_PLATFORM", platformErr)
		}
		versions, listErr := store.ListVersions(appstore.ListVersionsInput{
			Context: context.Background(), Account: account, App: input.App, Platform: platform,
		})
		if listErr != nil {
			fail(input, errorCode(listErr, false), listErr)
		}
		result.Versions = &versions
	case "purchase":
		platform, platformErr := appstore.ParsePlatform(input.Platform)
		if platformErr != nil {
			fail(input, "INVALID_PLATFORM", platformErr)
		}
		purchaseErr := store.Purchase(appstore.PurchaseInput{Account: account, App: input.App, Platform: platform})
		if purchaseErr != nil && !errors.Is(purchaseErr, appstore.ErrLicenseAlreadyExists) {
			fail(input, errorCode(purchaseErr, false), purchaseErr)
		}
	case "download":
		platform, platformErr := appstore.ParsePlatform(input.Platform)
		if platformErr != nil {
			fail(input, "INVALID_PLATFORM", platformErr)
		}
		bar := progressbar.NewOptions64(-1,
			progressbar.OptionSetWriter(os.Stderr),
			progressbar.OptionShowBytes(true),
			progressbar.OptionSetWidth(12),
			progressbar.OptionThrottle(250000000),
		)
		download, downloadErr := downloadAuthorizedPackage(store, appstore.DownloadInput{
			Context: context.Background(), Account: account, App: input.App,
			OutputPath: input.OutputPath, ExternalVersionID: input.VersionID,
			Platform: platform, Progress: bar,
		})
		if downloadErr != nil {
			fail(input, errorCode(downloadErr, false), downloadErr)
		}
		result.Download = &download
	default:
		fail(input, "INVALID_COMMAND", errors.New("unsupported Store helper command"))
	}

	account.Password = ""
	result.Account = &account
	result.Cookies = collectCookies(jar, append(cookieURLs, storeURLs(account.Pod)...))
	write(result, 0)
}

// Download returns the Apple archive and the account-specific FairPlay data
// separately. Match ipatool's CLI: inject that data before reporting success.
func downloadAuthorizedPackage(store appstore.AppStore, input appstore.DownloadInput) (appstore.DownloadOutput, error) {
	output, err := store.Download(input)
	if err != nil {
		return appstore.DownloadOutput{}, err
	}
	if err := store.ReplicateSinf(appstore.ReplicateSinfInput{
		Sinfs: output.Sinfs, PackagePath: output.DestinationPath,
	}); err != nil {
		return appstore.DownloadOutput{}, fmt.Errorf("failed to finalize IPA authorization: %w", err)
	}
	return output, nil
}

func macAddress(guid string) (string, error) {
	raw, err := hex.DecodeString(strings.ReplaceAll(guid, ":", ""))
	if err != nil || len(raw) != 6 {
		return "", errors.New("device GUID must contain exactly six bytes")
	}
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", raw[0], raw[1], raw[2], raw[3], raw[4], raw[5]), nil
}

func storeURLs(pod string) []*url.URL {
	hosts := []string{"https://itunes.apple.com/", "https://buy.itunes.apple.com/"}
	if pod != "" {
		hosts = append(hosts, "https://p"+pod+"-buy.itunes.apple.com/")
	}
	result := make([]*url.URL, 0, len(hosts))
	for _, value := range hosts {
		parsed, _ := url.Parse(value)
		result = append(result, parsed)
	}
	return result
}

func restoreCookies(jar memoryJar, cookies []*http.Cookie) []*url.URL {
	result := make([]*url.URL, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil || cookie.Name == "" || cookie.Domain == "" {
			continue
		}
		scheme := "https"
		if !cookie.Secure {
			scheme = "http"
		}
		path := cookie.Path
		if path == "" {
			path = "/"
		}
		target := &url.URL{Scheme: scheme, Host: strings.TrimPrefix(cookie.Domain, "."), Path: path}
		jar.SetCookies(target, []*http.Cookie{cookie})
		result = append(result, target)
	}
	return result
}

func collectCookies(jar memoryJar, targets []*url.URL) []*http.Cookie {
	seen := make(map[string]bool)
	result := make([]*http.Cookie, 0)
	for _, target := range targets {
		if target == nil {
			continue
		}
		for _, cookie := range jar.Cookies(target) {
			key := strings.Join([]string{cookie.Domain, cookie.Path, cookie.Name}, "|")
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, cookie)
		}
	}
	return result
}

func errorCode(err error, login bool) string {
	switch {
	case errors.Is(err, appstore.ErrAuthCodeRequired):
		return "AUTH_CODE_REQUIRED"
	case errors.Is(err, appstore.ErrPasswordTokenExpired):
		return "TOKEN_EXPIRED"
	case errors.Is(err, appstore.ErrLicenseRequired):
		return "LICENSE_REQUIRED"
	case errors.Is(err, appstore.ErrTemporarilyUnavailable):
		return "TEMPORARILY_UNAVAILABLE"
	case login:
		return "AUTH_FAILED"
	default:
		return "STORE_FAILED"
	}
}

func fail(input request, code string, err error) {
	message := err.Error()
	if input.Email != "" {
		message = strings.ReplaceAll(message, input.Email, "[Apple Account]")
	}
	write(response{OK: false, Code: code, Message: message}, 1)
}

func write(value response, exitCode int) {
	_ = json.NewEncoder(os.Stdout).Encode(value)
	os.Exit(exitCode)
}
