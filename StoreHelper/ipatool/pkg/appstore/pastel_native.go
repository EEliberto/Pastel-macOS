// Pastel extension to pinned ipatool v2.8.0. Network transfer is owned by Swift.
package appstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/majd/ipatool/v2/pkg/util/operatingsystem"
	"howett.net/plist"
	"os"
	"path/filepath"
	"strings"
)

type NativeDownloadPlan struct {
	URL        string `json:"url"`
	ArtworkURL string `json:"artworkURL"`
	MD5        string `json:"md5"`
	FileName   string `json:"fileName"`
	VersionID  string `json:"versionID"`
	Payload    []byte `json:"payload"`
}
type nativePayload struct {
	Metadata []byte
	Item     downloadItemResult
	Platform Platform
}

func PrepareNativeDownload(store AppStore, input DownloadInput) (NativeDownloadPlan, error) {
	t, ok := store.(*appstore)
	if !ok {
		return NativeDownloadPlan{}, errors.New("unsupported native Store adapter")
	}
	if input.Platform == PlatformMacOS {
		return NativeDownloadPlan{}, errors.New("native downloads currently support IPA packages")
	}
	macAddr, err := t.machine.MacAddress()
	if err != nil {
		return NativeDownloadPlan{}, fmt.Errorf("failed to get mac address: %w", err)
	}

	guid := strings.ReplaceAll(strings.ToUpper(macAddr), ":", "")

	var machineGUID []byte
	if input.Platform == PlatformMacOS {
		guid, machineGUID, err = machineIdentity(macAddr)
		if err != nil {
			return NativeDownloadPlan{}, fmt.Errorf("failed to resolve machine identity: %w", err)
		}
	}

	externalVersionID := input.ExternalVersionID
	if externalVersionID == "" && (input.Platform == PlatformAppleTV || input.Platform == PlatformWatchOS || input.Platform == PlatformVisionOS) {
		externalVersionID, err = t.lookupLatestExternalVersionID(input.Account, input.App, input.Platform)
		// Delisted tvOS apps may have no catalog offer but still be available
		// for redownload. Validate the returned package's platform below.
		missingTVOffer := input.Platform == PlatformAppleTV &&
			(errors.Is(err, errPlatformAppNotFound) || errors.Is(err, errPlatformOffersNotFound))
		if err != nil && !missingTVOffer {
			return NativeDownloadPlan{}, fmt.Errorf("failed to resolve platform version: %w", err)
		}
	}

	_ = machineGUID
	res, resolvedPlatform, err := t.sendDownloadProduct(input.Context, input.Account, input.App, guid, externalVersionID, input.Platform)
	if err != nil {
		return NativeDownloadPlan{}, err
	}

	if res.Data.FailureType == FailureTypePasswordTokenExpired ||
		res.Data.FailureType == FailureTypeSignInRequired ||
		res.Data.FailureType == FailureTypeDeviceVerificationFailed ||
		res.Data.FailureType == FailureTypeLicenseAlreadyExists {
		return NativeDownloadPlan{}, ErrPasswordTokenExpired
	}

	if res.Data.FailureType == FailureTypeLicenseNotFound {
		return NativeDownloadPlan{}, ErrLicenseRequired
	}

	if res.Data.CustomerMessage != "" && (res.Data.FailureType != "" || len(res.Data.Items) == 0) {
		return NativeDownloadPlan{}, NewErrorWithMetadata(fmt.Errorf("received error: %s", res.Data.CustomerMessage), res)
	}

	if res.Data.FailureType != "" {
		return NativeDownloadPlan{}, NewErrorWithMetadata(fmt.Errorf("received error: %s", res.Data.FailureType), res)
	}

	if len(res.Data.Items) == 0 {
		return NativeDownloadPlan{}, NewErrorWithMetadata(errors.New("invalid response"), res)
	}

	item := res.Data.Items[0]

	version := "unknown"

	// Read the version from the item metadata
	if itemVersion, ok := item.Metadata["bundleShortVersionString"]; ok {
		version = fmt.Sprintf("%v", itemVersion)
	}

	packagePlatform, err := downloadPackagePlatform(input.Platform, item)
	if err != nil {
		return NativeDownloadPlan{}, err
	}

	destination, err := t.resolveDestinationPath(input.App, version, input.OutputPath, packagePlatform)
	if err != nil {
		return NativeDownloadPlan{}, fmt.Errorf("failed to resolve destination path: %w", err)
	}

	metadata, err := plist.Marshal(item.Metadata, plist.BinaryFormat)
	if err != nil {
		return NativeDownloadPlan{}, err
	}
	item.Metadata = nil
	payload, err := json.Marshal(nativePayload{Item: item, Platform: resolvedPlatform, Metadata: metadata})
	if err != nil {
		return NativeDownloadPlan{}, err
	}
	return NativeDownloadPlan{URL: item.URL, ArtworkURL: item.ArtworkURL, MD5: item.HashMD5, FileName: filepath.Base(destination), VersionID: input.ExternalVersionID, Payload: payload}, nil
}

// Finalization is entirely local: no account lock, credentials or network requests.
func FinalizeNativeDownload(plan NativeDownloadPlan, account Account, rawPath, artworkPath, destination string) (DownloadOutput, error) {
	var payload nativePayload
	if err := json.Unmarshal(plan.Payload, &payload); err != nil {
		return DownloadOutput{}, err
	}
	if _, err := plist.Unmarshal(payload.Metadata, &payload.Item.Metadata); err != nil {
		return DownloadOutput{}, err
	}
	t := &appstore{os: operatingsystem.New()}
	if err := t.validatePackagePlatform(rawPath, payload.Platform); err != nil {
		return DownloadOutput{}, err
	}
	var artwork []byte
	var err error
	if artworkPath != "" {
		artwork, err = os.ReadFile(artworkPath)
		if err != nil {
			return DownloadOutput{}, err
		}
	}
	if err = t.applyPatches(payload.Item, account, rawPath, destination, artwork); err != nil {
		return DownloadOutput{}, err
	}
	// Upstream opens the SINF staging file without O_TRUNC. An interrupted
	// rewrite must not leave a longer old ZIP tail in the resumed output.
	if err = os.Remove(destination + ".tmp"); err != nil && !os.IsNotExist(err) {
		return DownloadOutput{}, err
	}
	if err = t.ReplicateSinf(ReplicateSinfInput{Sinfs: payload.Item.Sinfs, PackagePath: destination}); err != nil {
		return DownloadOutput{}, err
	}
	return DownloadOutput{DestinationPath: destination}, nil
}
