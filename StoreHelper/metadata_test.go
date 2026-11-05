package main

import (
	"archive/zip"
	"bytes"
	"howett.net/plist"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeMetadataPreservesIPAAndWritesCompatibleAccount(t *testing.T) {
	for _, removeUpdates := range []bool{false, true} {
		name := "normal"
		if removeUpdates {
			name = "no-update"
		}
		t.Run(name, func(t *testing.T) {
			for _, format := range []int{plist.BinaryFormat, plist.XMLFormat} {
				t.Run(map[int]string{plist.BinaryFormat: "binary", plist.XMLFormat: "xml"}[format], func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "fixture.ipa")
					metadata := map[string]interface{}{"apple-id": "old@example.invalid", "itemId": uint64(123), "com.apple.iTunesStore.downloadInfo": map[string]interface{}{"other": "preserve", "accountInfo": map[string]interface{}{"other": "also-preserve"}}}
					encoded, err := plist.Marshal(metadata, format)
					if err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(path)
					if err != nil {
						t.Fatal(err)
					}
					w := zip.NewWriter(f)
					originals := map[string][]byte{"Payload/Test.app/Test": []byte("encrypted-executable"), "Payload/Test.app/SC_Info/Test.sinf": []byte("valid-authorization"), "Payload/Test.app/_CodeSignature/CodeResources": []byte("unchanged-signature"), "iTunesMetadata.plist": encoded}
					for name, data := range originals {
						e, err := w.Create(name)
						if err != nil {
							t.Fatal(err)
						}
						if _, err = e.Write(data); err != nil {
							t.Fatal(err)
						}
					}
					if err = w.Close(); err != nil {
						t.Fatal(err)
					}
					f.Close()
					if err = normalizeAccountMetadata(path, "new@example.invalid", removeUpdates); err != nil {
						t.Fatal(err)
					}
					r, err := zip.OpenReader(path)
					if err != nil {
						t.Fatal(err)
					}
					defer r.Close()
					seen := map[string]bool{}
					metadataName := "iTunesMetadata.plist"
					if removeUpdates {
						metadataName = "PastelMetadata.plist"
					}
					for _, e := range r.File {
						if seen[e.Name] {
							t.Fatalf("duplicate entry: %s", e.Name)
						}
						seen[e.Name] = true
						rd, err := e.Open()
						if err != nil {
							t.Fatal(err)
						}
						data, err := io.ReadAll(rd)
						rd.Close()
						if err != nil {
							t.Fatal(err)
						}
						if e.Name != metadataName {
							if !bytes.Equal(data, originals[e.Name]) {
								t.Fatalf("changed payload: %s", e.Name)
							}
							continue
						}
						if !bytes.HasPrefix(data, []byte("<?xml")) {
							t.Fatal("metadata is not XML")
						}
						var got map[string]interface{}
						if _, err := plist.Unmarshal(data, &got); err != nil {
							t.Fatal(err)
						}
						if removeUpdates && (got["pastelRemovesAppStoreUpdates"] != true || got["pastelMetadataVersion"] != uint64(1)) {
							t.Fatal("missing no-update metadata markers")
						}
						for _, key := range []string{"appleId", "apple-id", "userName"} {
							if got[key] != "new@example.invalid" {
								t.Fatalf("wrong %s", key)
							}
						}
						info := got["com.apple.iTunesStore.downloadInfo"].(map[string]interface{})
						account := info["accountInfo"].(map[string]interface{})
						if account["AppleID"] != "new@example.invalid" || account["other"] != "also-preserve" || info["other"] != "preserve" || got["itemId"] != uint64(123) {
							t.Fatal("lost metadata")
						}
					}
					if len(seen) != len(originals) {
						t.Fatal("lost archive entries")
					}
					if !seen[metadataName] || (removeUpdates && seen["iTunesMetadata.plist"]) || (!removeUpdates && seen["PastelMetadata.plist"]) {
						t.Fatal("incorrect update metadata layout")
					}
				})
			}
		})
	}
}

func TestMetadataFailurePreservesOriginalArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.ipa")
	original := []byte("not-a-zip")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := normalizeAccountMetadata(path, "test@example.invalid", false); err == nil {
		t.Fatal("accepted invalid IPA")
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, original) {
		t.Fatal("destroyed original")
	}
}
