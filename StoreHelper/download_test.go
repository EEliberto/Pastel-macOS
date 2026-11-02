package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/majd/ipatool/v2/pkg/appstore"
	"github.com/majd/ipatool/v2/pkg/util/operatingsystem"
)

type fixtureStore struct {
	appstore.AppStore
	output      appstore.DownloadOutput
	downloadErr error
	patchErr    error
	patchCalled bool
}

func (s *fixtureStore) Download(appstore.DownloadInput) (appstore.DownloadOutput, error) {
	return s.output, s.downloadErr
}

func (s *fixtureStore) ReplicateSinf(input appstore.ReplicateSinfInput) error {
	s.patchCalled = true
	if s.patchErr != nil {
		return s.patchErr
	}
	return s.AppStore.ReplicateSinf(input)
}

func TestDownloadedIPAContainsAccountAuthorization(t *testing.T) {
	for _, manifest := range []bool{false, true} {
		name := "InfoPlist"
		if manifest {
			name = "Manifest"
		}
		t.Run(name, func(t *testing.T) {
			ipa := filepath.Join(t.TempDir(), "test.ipa")
			file, err := os.Create(ipa)
			if err != nil {
				t.Fatal(err)
			}
			writer := zip.NewWriter(file)
			entries := map[string]string{
				"Payload/Test.app/Info.plist": `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleExecutable</key><string>Test</string></dict></plist>`,
				"Payload/Test.app/Test":       "original-encrypted-executable",
			}
			if manifest {
				entries["Payload/Test.app/SC_Info/Manifest.plist"] = `<?xml version="1.0"?><plist version="1.0"><dict><key>SinfPaths</key><array><string>SC_Info/Test.sinf</string></array></dict></plist>`
			}
			for path, data := range entries {
				entry, err := writer.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(entry, data); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			authorization := []byte("account-specific-authorization-fixture")
			store := &fixtureStore{
				AppStore: appstore.NewAppStore(appstore.Args{OperatingSystem: operatingsystem.New()}),
				output:   appstore.DownloadOutput{DestinationPath: ipa, Sinfs: []appstore.Sinf{{Data: authorization}}},
			}
			output, err := downloadAuthorizedPackage(store, appstore.DownloadInput{})
			if err != nil {
				t.Fatal(err)
			}
			archive, err := zip.OpenReader(output.DestinationPath)
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()
			found := false
			for _, entry := range archive.File {
				reader, err := entry.Open()
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(reader)
				reader.Close()
				if err != nil {
					t.Fatal(err)
				}
				if entry.Name == "Payload/Test.app/SC_Info/Test.sinf" {
					found = true
					if !bytes.Equal(data, authorization) {
						t.Fatal("wrong account authorization")
					}
				}
				if expected, ok := entries[entry.Name]; ok && string(data) != expected {
					t.Fatalf("original content changed: %s", entry.Name)
				}
			}
			if !found {
				t.Fatal("download succeeded without SC_Info/Test.sinf")
			}
		})
	}
}

func TestDownloadOrAuthorizationFailureCannotReturnSuccess(t *testing.T) {
	want := errors.New("test failure")
	for _, downloadFails := range []bool{true, false} {
		store := &fixtureStore{output: appstore.DownloadOutput{DestinationPath: "unfinalized.ipa"}}
		if downloadFails {
			store.downloadErr = want
		} else {
			store.patchErr = want
		}
		output, err := downloadAuthorizedPackage(store, appstore.DownloadInput{})
		if !errors.Is(err, want) || output.DestinationPath != "" {
			t.Fatal("failure exposed a successful download")
		}
		if store.patchCalled == downloadFails {
			t.Fatal("incorrect authorization step ordering")
		}
	}
}
