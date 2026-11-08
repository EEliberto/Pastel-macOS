package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const anisetteStateKey = "anisette-v4"
const stateFormat = "pastel-store-auth-v1"

// Only device provisioning and account-scoped cookies are persisted. Passwords remain in
// memory; the encryption key arrives through stdin from Pastel's Keychain vault.
// The lock protects only authentication and download authorization. It is
// released before opening the package file, so transfers run concurrently.
type authenticationKeychain struct {
	mu           sync.Mutex
	values       map[string][]byte
	path         string
	aead         cipher.AEAD
	lock         *os.File
	transferring bool
}

func authenticationStateDirectory() string {
	root := os.Getenv("IPA_SESSION_DIR")
	if root == "" {
		config, err := os.UserConfigDir()
		if err != nil {
			return ""
		}
		root = filepath.Join(config, "IPA Download", "sessions")
	}
	return filepath.Join(root, "store-auth")
}

func newAuthenticationKeychain(directory, deviceID, encodedKey string) (*authenticationKeychain, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("a 256-bit session encryption key is required for Apple device authentication")
	}
	if directory == "" {
		return nil, errors.New("Apple authentication storage directory is unavailable")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return nil, fmt.Errorf("create Apple authentication storage: %w", err)
	}
	if err = os.Chmod(directory, 0700); err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(deviceID))
	name := filepath.Join(directory, fmt.Sprintf("%x", digest))
	lock, err := os.OpenFile(name+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = acquireStateLock(lock, 20*time.Second); err != nil {
		lock.Close()
		return nil, fmt.Errorf("lock Apple authentication state: %w", err)
	}
	k := &authenticationKeychain{values: make(map[string][]byte), path: name + ".enc", aead: aead, lock: lock}
	data, err := os.ReadFile(k.path)
	if errors.Is(err, os.ErrNotExist) {
		return k, nil
	}
	if err != nil {
		k.Close()
		return nil, fmt.Errorf("read Apple authentication state: %w", err)
	}
	size := aead.NonceSize()
	if len(data) < size+aead.Overhead() {
		k.Close()
		return nil, errors.New("Apple authentication state is damaged")
	}
	state, err := aead.Open(nil, data[:size], data[size:], k.associatedData())
	if err != nil {
		k.Close()
		return nil, errors.New("unable to decrypt Apple authentication state with the current Keychain key")
	}
	if err = json.Unmarshal(state, &k.values); err != nil || k.values == nil {
		k.Close()
		return nil, errors.New("Apple authentication state has an invalid format")
	}
	for key := range k.values {
		if !persistentAuthenticationKey(key) {
			delete(k.values, key)
		}
	}
	return k, nil
}

var errAuthenticationBusy = errors.New("another task is updating Apple authentication; try again shortly")

func acquireStateLock(file *os.File, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return err
		}
		if !time.Now().Before(deadline) {
			return errAuthenticationBusy
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// No shared state may be written from this stale snapshot after another helper
// has acquired the lock. Cookies are committed before this boundary, and CDN
// cookies received during transfer are not Store authentication cookies.
func (k *authenticationKeychain) releaseForTransfer() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.transferring {
		return nil
	}
	k.transferring = true
	return k.closeLocked()
}

func (k *authenticationKeychain) associatedData() []byte {
	return []byte(stateFormat + ":" + filepath.Base(k.path))
}

func (k *authenticationKeychain) Get(key string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	value, ok := k.values[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), value...), nil
}

func persistentAuthenticationKey(key string) bool {
	return key == anisetteStateKey || key == "kbsync-v1" || strings.HasPrefix(key, "cookies/")
}

func (k *authenticationKeychain) persist(changedKey string, value []byte, remove bool) error {
	values := make(map[string][]byte)
	for key, saved := range k.values {
		if persistentAuthenticationKey(key) {
			values[key] = saved
		}
	}
	if remove {
		delete(values, changedKey)
	} else {
		values[changedKey] = value
	}
	plaintext, err := json.Marshal(values)
	if err != nil {
		return err
	}
	nonce := make([]byte, k.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	data := k.aead.Seal(nonce, nonce, plaintext, k.associatedData())
	temporary, err := os.CreateTemp(filepath.Dir(k.path), ".auth-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err = temporary.Write(data); err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temporary.Name(), k.path)
}

func (k *authenticationKeychain) Set(key string, value []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if persistentAuthenticationKey(key) {
		if k.transferring {
			return errors.New("cannot update Apple authentication after transfer started")
		}
		if err := k.persist(key, value, false); err != nil {
			return err
		}
	}
	k.values[key] = append([]byte(nil), value...)
	return nil
}

func (k *authenticationKeychain) Remove(key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if persistentAuthenticationKey(key) {
		if k.transferring {
			return errors.New("cannot remove Apple authentication after transfer started")
		}
		if err := k.persist(key, nil, true); err != nil {
			return err
		}
	}
	delete(k.values, key)
	return nil
}

func (k *authenticationKeychain) Close() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.closeLocked()
}

func (k *authenticationKeychain) closeLocked() error {
	if k.lock == nil {
		return nil
	}
	err := k.lock.Close()
	k.lock = nil
	return err
}
