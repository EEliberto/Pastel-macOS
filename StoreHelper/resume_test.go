package main

import (
	"github.com/majd/ipatool/v2/pkg/util/operatingsystem"
	"os"
	"path/filepath"
	"testing"
)

func TestManagedResumeKeepsRawArchiveUntilWorkspacePublishes(t *testing.T) {
	for _, managed := range []bool{false, true} {
		directory := t.TempDir()
		archive := filepath.Join(directory, "app.ipa.tmp")
		if err := os.WriteFile(archive, []byte("downloaded bytes"), 0600); err != nil {
			t.Fatal(err)
		}
		fs := &transferOperatingSystem{OperatingSystem: operatingsystem.New(), preserveDownloadFiles: managed}
		if err := fs.Remove(archive); err != nil {
			t.Fatal(err)
		}
		_, err := os.Stat(archive)
		if managed && err != nil {
			t.Fatal("managed raw archive lost before packaging completes")
		}
		if !managed && !os.IsNotExist(err) {
			t.Fatal("unmanaged archive was retained")
		}
		other := filepath.Join(directory, "other.tmp")
		if err := os.WriteFile(other, []byte("temporary"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := fs.Remove(other); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(other); !os.IsNotExist(err) {
			t.Fatal("unrelated cleanup was intercepted")
		}
	}
}
