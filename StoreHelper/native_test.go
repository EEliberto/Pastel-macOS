package main

import (
	"archive/zip"
	"encoding/json"
	"github.com/majd/ipatool/v2/pkg/appstore"
	"howett.net/plist"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeFinalizationPreservesMetadataTypesAndRawArchive(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "download.ipa.tmp")
	output := filepath.Join(dir, "final.ipa")
	file, err := os.Create(raw)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	info, _ := plist.Marshal(map[string]interface{}{"CFBundleExecutable": "Fixture", "CFBundleIdentifier": "example.fixture", "CFBundleSupportedPlatforms": []string{"iPhoneOS"}}, plist.XMLFormat)
	for name, data := range map[string][]byte{"Payload/Fixture.app/Info.plist": info, "Payload/Fixture.app/Fixture": []byte("fixture executable")} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	file.Close()
	metadata, err := plist.Marshal(map[string]interface{}{"softwareVersionExternalIdentifier": uint64(894373963), "bundleShortVersionString": "1.0"}, plist.BinaryFormat)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]interface{}{"Item": map[string]interface{}{"Sinfs": []appstore.Sinf{{ID: 1, Data: []byte("fixture-license")}}}, "Platform": appstore.PlatformIPhone, "Metadata": metadata})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(output+".tmp", make([]byte, 1024*1024), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = appstore.FinalizeNativeDownload(appstore.NativeDownloadPlan{Payload: payload}, appstore.Account{Email: "fixture@example.invalid"}, raw, "", output)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(raw); err != nil {
		t.Fatal("finalization deleted resumable archive")
	}
	archive, err := zip.OpenReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	foundMetadata, foundLicense := false, false
	for _, entry := range archive.File {
		if entry.Name == "iTunesMetadata.plist" {
			stream, _ := entry.Open()
			data, _ := io.ReadAll(stream)
			stream.Close()
			var values map[string]interface{}
			if _, err = plist.Unmarshal(data, &values); err != nil {
				t.Fatal(err)
			}
			if values["softwareVersionExternalIdentifier"] != uint64(894373963) {
				t.Fatalf("integer metadata changed type: %T", values["softwareVersionExternalIdentifier"])
			}
			foundMetadata = true
		}
		if entry.Name == "Payload/Fixture.app/SC_Info/Fixture.sinf" {
			foundLicense = true
		}
	}
	if !foundMetadata || !foundLicense {
		t.Fatal("missing metadata or license")
	}
}
