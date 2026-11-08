package main

import (
	"encoding/json"
	"errors"
	"net"

	"github.com/majd/ipatool/v2/pkg/appstore"
	apphttp "github.com/majd/ipatool/v2/pkg/http"
)

func loginErrorCode(err error, authCode string) string {
	if errors.Is(err, appstore.ErrAuthCodeRequired) {
		return "AUTH_CODE_REQUIRED"
	}
	if errors.Is(err, appstore.ErrPasswordTokenExpired) {
		return "TOKEN_EXPIRED"
	}
	if errors.Is(err, appstore.ErrTemporarilyUnavailable) {
		return "AUTH_SERVICE_ERROR"
	}

	var responseErr *apphttp.UnexpectedResponseError
	var transportErr *apphttp.TransportError
	var networkErr net.Error
	if errors.As(err, &responseErr) || errors.As(err, &transportErr) || errors.As(err, &networkErr) {
		return "AUTH_SERVICE_ERROR"
	}
	// These errors are not exported sentinels in the pinned upstream version.
	switch err.Error() {
	case "apple did not complete verification; try a fresh 2FA code", "2FA code must contain exactly six digits":
		return "AUTH_CODE_INVALID"
	case "account is disabled":
		return "ACCOUNT_DISABLED"
	}

	// Upstream's login result type is private, but Error.Metadata exposes its
	// structured response. Inspect only the failure code, never log metadata or
	// infer a credential failure from HTTP status or localized message text.
	var appleError *appstore.Error
	if errors.As(err, &appleError) {
		var result struct{ Data struct{ FailureType string } }
		data, encodeErr := json.Marshal(appleError.Metadata)
		if encodeErr == nil && json.Unmarshal(data, &result) == nil && result.Data.FailureType == appstore.FailureTypeInvalidCredentials {
			if authCode != "" {
				// The server saw password+code; it cannot identify which part
				// was rejected. Preserve that distinction from a bad password.
				return "AUTH_VERIFICATION_FAILED"
			}
			return "INVALID_CREDENTIALS"
		}
	}
	return "AUTH_FAILED"
}
