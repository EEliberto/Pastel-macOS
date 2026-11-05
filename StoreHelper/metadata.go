package main

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"howett.net/plist"
)

// Restore Pastel's account metadata contract without modifying signed app data.
// Use a sibling temporary file so a failed rewrite preserves the original IPA.
func normalizeAccountMetadata(packagePath, email string, removeUpdates bool) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return errors.New("missing authenticated Apple account")
	}
	source, err := zip.OpenReader(packagePath)
	if err != nil {
		return err
	}
	defer source.Close()
	var metadata map[string]interface{}
	for _, entry := range source.File {
		if entry.Name != "iTunesMetadata.plist" {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if _, err := plist.Unmarshal(data, &metadata); err != nil {
			return err
		}
		break
	}
	if metadata == nil {
		return errors.New("missing iTunesMetadata.plist")
	}
	metadata["apple-id"] = email
	metadata["userName"] = email
	metadata["appleId"] = email
	downloadInfo, _ := metadata["com.apple.iTunesStore.downloadInfo"].(map[string]interface{})
	if downloadInfo == nil {
		downloadInfo = make(map[string]interface{})
	}
	accountInfo, _ := downloadInfo["accountInfo"].(map[string]interface{})
	if accountInfo == nil {
		accountInfo = make(map[string]interface{})
	}
	accountInfo["AppleID"] = email
	downloadInfo["accountInfo"] = accountInfo
	metadata["com.apple.iTunesStore.downloadInfo"] = downloadInfo
	metadataName := "iTunesMetadata.plist"
	if removeUpdates {
		metadataName = "PastelMetadata.plist"
		metadata["pastelMetadataVersion"] = uint64(1)
		metadata["pastelRemovesAppStoreUpdates"] = true
	}
	data, err := plist.Marshal(metadata, plist.XMLFormat)
	if err != nil {
		return err
	}

	temporary, err := os.CreateTemp(filepath.Dir(packagePath), ".pastel-metadata-*.ipa")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	defer temporary.Close()
	if info, err := os.Stat(packagePath); err != nil {
		return err
	} else if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	archive := zip.NewWriter(temporary)
	for _, entry := range source.File {
		if entry.Name == "iTunesMetadata.plist" || entry.Name == "PastelMetadata.plist" {
			continue
		}
		if err := archive.Copy(entry); err != nil {
			archive.Close()
			return fmt.Errorf("copy IPA entry: %w", err)
		}
	}
	entry, err := archive.Create(metadataName)
	if err != nil {
		archive.Close()
		return err
	}
	if _, err := entry.Write(data); err != nil {
		archive.Close()
		return err
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := source.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, packagePath)
}
