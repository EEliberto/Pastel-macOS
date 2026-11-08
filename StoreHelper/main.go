package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/majd/ipatool/v2/pkg/appstore"
	"github.com/majd/ipatool/v2/pkg/util/operatingsystem"
	"github.com/schollz/progressbar/v3"
)

type request struct {
	Command                      string           `json:"command"`
	Email                        string           `json:"email,omitempty"`
	Password                     string           `json:"password,omitempty"`
	Code                         string           `json:"code,omitempty"`
	GUID                         string           `json:"guid"`
	SessionKey                   string           `json:"sessionKey,omitempty"`
	Account                      appstore.Account `json:"account,omitempty"`
	CookieState                  []storedCookie   `json:"cookieState,omitempty"`
	App                          appstore.App     `json:"app,omitempty"`
	Platform                     string           `json:"platform,omitempty"`
	VersionID                    string           `json:"versionId,omitempty"`
	OutputPath                   string           `json:"outputPath,omitempty"`
	RemoveAppStoreUpdateMetadata bool             `json:"removeAppStoreUpdateMetadata,omitempty"`
}

type response struct {
	OK          bool                         `json:"ok"`
	Code        string                       `json:"code,omitempty"`
	Message     string                       `json:"message,omitempty"`
	Account     *appstore.Account            `json:"account,omitempty"`
	CookieState []storedCookie               `json:"cookieState,omitempty"`
	Versions    *appstore.ListVersionsOutput `json:"versions,omitempty"`
	Download    *appstore.DownloadOutput     `json:"download,omitempty"`
}

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

	emitPhase("waiting")
	keys, err := newAuthenticationKeychain(authenticationStateDirectory(), mac, input.SessionKey)
	if err != nil {
		if errors.Is(err, errAuthenticationBusy) {
			fail(input, "AUTH_STATE_BUSY", err)
		}
		fail(input, "AUTH_STATE_FAILED", err)
	}
	defer keys.Close()
	jar, err := restoreAuthenticationCookies(keys, input.Email, input.CookieState)
	if err != nil {
		fail(input, "AUTH_STATE_FAILED", err)
	}
	abort := func(code string, cause error) {
		if err := saveAuthenticationCookies(keys, input.Email, jar); err != nil {
			fail(input, "AUTH_STATE_FAILED", err)
		}
		fail(input, code, cause)
	}

	if input.Account.PasswordToken != "" && input.Account.DirectoryServicesID != "" {
		encoded, encodeErr := json.Marshal(input.Account)
		if encodeErr != nil {
			abort("INVALID_SESSION", encodeErr)
		}
		keys.values["account"] = encoded
	}

	emitPhase("authenticating")
	if !appleAssetsCached() {
		emitPhase("preparing")
	}
	fileSystem := &transferOperatingSystem{OperatingSystem: operatingsystem.New(), beforeTransfer: func() error {
		if err := saveAuthenticationCookies(keys, input.Email, jar); err != nil {
			return err
		}
		if err := keys.releaseForTransfer(); err != nil {
			return err
		}
		emitPhase("transferring")
		return nil
	}}
	store := appstore.NewAppStore(appstore.Args{
		Keychain:        keys,
		CookieJar:       jar,
		OperatingSystem: fileSystem,
		Machine:         fixedMachine{mac: mac},
	})

	account := input.Account
	result := response{OK: true}
	if input.Command != "login" && !canReuseAccount(input, keys) {
		login, loginErr := store.Login(appstore.LoginInput{
			Email: input.Email, Password: input.Password, AuthCode: input.Code,
		})
		if loginErr != nil {
			abort(loginErrorCode(loginErr, input.Code), loginErr)
		}
		account = login.Account
	}
	switch input.Command {
	case "login":
		login, loginErr := store.Login(appstore.LoginInput{
			Email: input.Email, Password: input.Password, AuthCode: input.Code,
		})
		if loginErr != nil {
			abort(loginErrorCode(loginErr, input.Code), loginErr)
		}
		account = login.Account
	case "listVersions":
		emitPhase("authorizing")
		platform, platformErr := appstore.ParsePlatform(input.Platform)
		if platformErr != nil {
			abort("INVALID_PLATFORM", platformErr)
		}
		versions, listErr := store.ListVersions(appstore.ListVersionsInput{
			Context: context.Background(), Account: account, App: input.App, Platform: platform,
		})
		if listErr != nil {
			abort(errorCode(listErr), listErr)
		}
		result.Versions = &versions
	case "purchase":
		emitPhase("authorizing")
		platform, platformErr := appstore.ParsePlatform(input.Platform)
		if platformErr != nil {
			abort("INVALID_PLATFORM", platformErr)
		}
		purchaseErr := store.Purchase(appstore.PurchaseInput{Account: account, App: input.App, Platform: platform})
		if purchaseErr != nil && !errors.Is(purchaseErr, appstore.ErrLicenseAlreadyExists) {
			abort(errorCode(purchaseErr), purchaseErr)
		}
	case "download":
		emitPhase("authorizing")
		platform, platformErr := appstore.ParsePlatform(input.Platform)
		if platformErr != nil {
			abort("INVALID_PLATFORM", platformErr)
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
		}, input.RemoveAppStoreUpdateMetadata)
		if downloadErr != nil {
			abort(errorCode(downloadErr), downloadErr)
		}
		result.Download = &download
	default:
		abort("INVALID_COMMAND", errors.New("unsupported Store helper command"))
	}

	account.Password = ""
	result.Account = &account
	result.CookieState = jar.snapshot()
	if err := saveAuthenticationCookies(keys, input.Email, jar); err != nil {
		fail(input, "AUTH_STATE_FAILED", err)
	}
	write(result, 0)
}

// Download returns the Apple archive and the account-specific FairPlay data
// separately. Match ipatool's CLI: inject that data before reporting success.
func downloadAuthorizedPackage(store appstore.AppStore, input appstore.DownloadInput, removeUpdates bool) (appstore.DownloadOutput, error) {
	originalOutputPath := input.OutputPath
	if removeUpdates {
		directory := originalOutputPath
		if directory == "" {
			directory = "."
		}
		if info, err := os.Stat(directory); err != nil || !info.IsDir() {
			directory = filepath.Dir(directory)
		}
		staging, err := os.MkdirTemp(directory, ".pastel-no-update-*")
		if err != nil {
			return appstore.DownloadOutput{}, err
		}
		defer os.RemoveAll(staging)
		input.OutputPath = staging
	}
	output, err := store.Download(input)
	if err != nil {
		return appstore.DownloadOutput{}, err
	}
	emitPhase("packaging")
	if err := store.ReplicateSinf(appstore.ReplicateSinfInput{
		Sinfs: output.Sinfs, PackagePath: output.DestinationPath,
	}); err != nil {
		return appstore.DownloadOutput{}, fmt.Errorf("failed to finalize IPA authorization: %w", err)
	}
	if err := normalizeAccountMetadata(output.DestinationPath, input.Account.Email, removeUpdates); err != nil {
		return appstore.DownloadOutput{}, fmt.Errorf("failed to finalize IPA account metadata: %w", err)
	}
	if removeUpdates {
		target := originalOutputPath
		if target == "" {
			target = "."
		}
		if info, err := os.Stat(target); err == nil && info.IsDir() {
			target = filepath.Join(target, filepath.Base(output.DestinationPath))
		}
		ext := filepath.Ext(target)
		target = strings.TrimSuffix(target, ext) + "_no-update" + ext
		if err := os.Rename(output.DestinationPath, target); err != nil {
			return appstore.DownloadOutput{}, err
		}
		output.DestinationPath = target
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

func canReuseAccount(input request, keys *authenticationKeychain) bool {
	state, err := keys.Get(anisetteStateKey)
	return err == nil && len(state) > 0 && input.Account.PasswordToken != "" && input.Account.DirectoryServicesID != "" &&
		input.Account.StoreFront != "" && strings.EqualFold(input.Account.Email, input.Email)
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, appstore.ErrAuthCodeRequired):
		return "AUTH_CODE_REQUIRED"
	case errors.Is(err, appstore.ErrPasswordTokenExpired):
		return "TOKEN_EXPIRED"
	case errors.Is(err, appstore.ErrLicenseRequired):
		return "LICENSE_REQUIRED"
	case errors.Is(err, appstore.ErrTemporarilyUnavailable):
		return "TEMPORARILY_UNAVAILABLE"
	default:
		return "STORE_FAILED"
	}
}

func fail(input request, code string, err error) {
	write(response{OK: false, Code: code, Message: safeErrorMessage(input, err)}, 1)
}

func safeErrorMessage(input request, err error) string {
	message := err.Error()
	// Never serialize upstream Error.Metadata: it may contain tokens/cookies.
	for _, secret := range []string{input.Password + input.Code, input.Password, input.Code, input.SessionKey, input.Account.PasswordToken, input.Email} {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	return message
}

func write(value response, exitCode int) {
	_ = json.NewEncoder(os.Stdout).Encode(value)
	os.Exit(exitCode)
}
