// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

// Package secrets keeps API keys in the OS keychain (macOS Keychain,
// Windows Credential Manager, the Secret Service on Linux). Config files
// refer to them as "keychain:<name>".
package secrets

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// service names hm's entries in the keychain.
const service = "hm"

// ErrNotFound means no secret is stored under the name.
var ErrNotFound = errors.New("no secret stored under that name")

// Store reads and writes secrets. Tests swap it for a map.
type Store interface {
	Get(name string) (string, error)
	Set(name, value string) error
	Delete(name string) error
}

// OS is the operating system's keychain.
type OS struct{}

func (OS) Get(name string) (string, error) {
	v, err := keyring.Get(service, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return v, err
}

func (OS) Set(name, value string) error { return keyring.Set(service, name, value) }

func (OS) Delete(name string) error {
	err := keyring.Delete(service, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Memory is a Store for tests.
type Memory map[string]string

func (m Memory) Get(name string) (string, error) {
	if v, ok := m[name]; ok {
		return v, nil
	}
	return "", ErrNotFound
}

func (m Memory) Set(name, value string) error { m[name] = value; return nil }
func (m Memory) Delete(name string) error     { delete(m, name); return nil }
