/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package proxy

import (
	"crypto/sha256"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeSecurity stands in for the security tool, so no test reaches a real keychain. It
// keeps the arguments of each call and answers a subcommand from results, or with
// nothing and no error.
type fakeSecurity struct {
	calls   [][]string
	results map[string]fakeSecurityResult
}

type fakeSecurityResult struct {
	output string
	err    error
}

func (f *fakeSecurity) run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	result := f.results[args[0]]
	return []byte(result.output), result.err
}

const loginKeychainPath = "/Users/me/Library/Keychains/login.keychain-db"

// The form `security login-keychain` prints the path in.
var printedLoginKeychain = fakeSecurityResult{output: "    \"" + loginKeychainPath + "\"\n"}

var errNotTrusted = errors.New("exit status 1")

func TestKeychainTrustCAAddsACATheSystemDoesNotTrust(t *testing.T) {
	security := &fakeSecurity{results: map[string]fakeSecurityResult{
		"verify-cert":    {output: "Cert Verify Result: CSSMERR_TP_NOT_TRUSTED\n", err: errNotTrusted},
		"login-keychain": printedLoginKeychain,
	}}
	announced := 0
	trust := CATrust{OnTerminal: true, Announce: func() {
		announced++
		if len(security.calls) != 2 {
			t.Errorf("announced after %d calls of the tool, want it right before the one that prompts", len(security.calls))
		}
	}}

	added, err := Keychain{GOOS: "darwin", Security: security.run}.TrustCA("/data/ca.crt", trust)
	if !added || err != nil || announced != 1 {
		t.Errorf("TrustCA = %v, %v, announced %d times, want the CA added and announced once", added, err, announced)
	}
	want := [][]string{
		{"verify-cert", "-c", "/data/ca.crt", "-p", "basic"},
		{"login-keychain"},
		{"add-trusted-cert", "-r", "trustRoot", "-p", "ssl", "-p", "basic", "-k", loginKeychainPath, "/data/ca.crt"},
	}
	if !reflect.DeepEqual(security.calls, want) {
		t.Errorf("the security tool was run with %q, want %q", security.calls, want)
	}
}

func TestKeychainTrustCALeavesACATheSystemAlreadyTrusts(t *testing.T) {
	security := &fakeSecurity{}
	trust := CATrust{OnTerminal: true, Announce: func() { t.Error("announced a prompt, want none for a CA that is trusted") }}

	added, err := Keychain{GOOS: "darwin", Security: security.run}.TrustCA("/data/ca.crt", trust)
	if added || err != nil {
		t.Errorf("TrustCA = %v, %v, want nothing added and no error", added, err)
	}
	if want := [][]string{{"verify-cert", "-c", "/data/ca.crt", "-p", "basic"}}; !reflect.DeepEqual(security.calls, want) {
		t.Errorf("the security tool was run with %q, want the one question, %q", security.calls, want)
	}
}

// A prompt the user cancels, and one nobody answers, both end the tool with an error.
func TestKeychainTrustCAReportsAPromptThatWasNotApproved(t *testing.T) {
	timedOut := errors.New("the command did not finish in time")
	for _, tc := range []struct {
		name   string
		result fakeSecurityResult
		want   string
	}{
		{"cancelled", fakeSecurityResult{output: "SecTrustSettingsSetTrustSettings: The authorization was canceled by the user.\n", err: errNotTrusted}, "exit status 1: SecTrustSettingsSetTrustSettings: The authorization was canceled by the user."},
		{"not answered", fakeSecurityResult{err: timedOut}, "the command did not finish in time"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			security := &fakeSecurity{results: map[string]fakeSecurityResult{
				"verify-cert":      {err: errNotTrusted},
				"login-keychain":   printedLoginKeychain,
				"add-trusted-cert": tc.result,
			}}

			added, err := Keychain{GOOS: "darwin", Security: security.run}.TrustCA("/data/ca.crt", CATrust{OnTerminal: true, Announce: func() {}})
			if added || err == nil || err.Error() != tc.want || !errors.Is(err, tc.result.err) {
				t.Errorf("TrustCA = %v, %v, want nothing added and the error %q", added, err, tc.want)
			}
		})
	}
}

func TestKeychainTrustCAReportsALoginKeychainItCannotFind(t *testing.T) {
	security := &fakeSecurity{results: map[string]fakeSecurityResult{
		"verify-cert":    {err: errNotTrusted},
		"login-keychain": {output: "security: SecKeychainCopyLogin: The specified keychain could not be found.\n", err: errNotTrusted},
	}}
	trust := CATrust{OnTerminal: true, Announce: func() { t.Error("announced a prompt, want none without a keychain to add to") }}

	added, err := Keychain{GOOS: "darwin", Security: security.run}.TrustCA("/data/ca.crt", trust)
	if added || err == nil || !strings.Contains(err.Error(), "The specified keychain could not be found.") || len(security.calls) != 2 {
		t.Errorf("TrustCA = %v, %v after %q, want what the tool printed and no certificate added", added, err, security.calls)
	}
}

func TestKeychainTrustCADoesNothingWhereNobodyCanAnswerOrTheUserDeclined(t *testing.T) {
	for _, tc := range []struct {
		name  string
		goos  string
		trust CATrust
	}{
		{"not macOS", "linux", CATrust{OnTerminal: true}},
		{"Windows", "windows", CATrust{OnTerminal: true}},
		{"not on a terminal", "darwin", CATrust{}},
		{"--no-ca-trust", "darwin", CATrust{OnTerminal: true, Disabled: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			security := &fakeSecurity{results: map[string]fakeSecurityResult{"verify-cert": {err: errNotTrusted}, "login-keychain": printedLoginKeychain}}
			trust := tc.trust
			trust.Announce = func() { t.Error("announced a prompt, want none") }

			added, err := Keychain{GOOS: tc.goos, Security: security.run}.TrustCA("/data/ca.crt", trust)
			if added || err != nil || len(security.calls) != 0 {
				t.Errorf("TrustCA = %v, %v, the security tool was run with %q, want nothing done", added, err, security.calls)
			}
		})
	}
}

// writeCA writes a file in the form of ca.crt and returns its path with the hash the
// security tool prints for that certificate.
func writeCA(t *testing.T) (path, hash string) {
	t.Helper()
	der := []byte("the bytes of an agent's CA")
	path = filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, fmt.Sprintf("%X", sha256.Sum256(der))
}

// Every agent's CA has the same name, so the one to remove is named by its hash.
func TestKeychainForgetCARemovesTheCAByItsHash(t *testing.T) {
	caPath, hash := writeCA(t)
	security := &fakeSecurity{results: map[string]fakeSecurityResult{
		"login-keychain":   printedLoginKeychain,
		"find-certificate": {output: "SHA-256 hash: " + strings.Repeat("0", 64) + "\nSHA-1 hash: 00\nSHA-256 hash: " + hash + "\nSHA-1 hash: 11\n"},
	}}
	announced := 0

	removed, err := Keychain{GOOS: "darwin", Security: security.run}.ForgetCA(caPath, func() { announced++ })
	if !removed || err != nil || announced != 1 {
		t.Errorf("ForgetCA = %v, %v, announced %d times, want the CA removed and announced once", removed, err, announced)
	}
	want := [][]string{
		{"login-keychain"},
		{"find-certificate", "-a", "-Z", loginKeychainPath},
		{"delete-certificate", "-Z", hash, "-t", loginKeychainPath},
	}
	if !reflect.DeepEqual(security.calls, want) {
		t.Errorf("the security tool was run with %q, want %q", security.calls, want)
	}
}

func TestKeychainForgetCADoesNothingForACATheKeychainDoesNotHold(t *testing.T) {
	caPath, _ := writeCA(t)
	noLoginKeychain := fakeSecurityResult{output: "security: SecKeychainCopyLogin: The specified keychain could not be found.\n", err: errNotTrusted}
	for _, tc := range []struct {
		name          string
		goos          string
		caPath        string
		loginKeychain fakeSecurityResult
		calls         int
	}{
		{"another agent's CA is there", "darwin", caPath, printedLoginKeychain, 2},
		{"the user has no login keychain", "darwin", caPath, noLoginKeychain, 1},
		{"the proxy never wrote a CA", "darwin", filepath.Join(t.TempDir(), "ca.crt"), printedLoginKeychain, 0},
		{"not macOS", "linux", caPath, printedLoginKeychain, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			security := &fakeSecurity{results: map[string]fakeSecurityResult{
				"login-keychain":   tc.loginKeychain,
				"find-certificate": {output: "SHA-256 hash: " + strings.Repeat("0", 64) + "\n"},
			}}

			removed, err := Keychain{GOOS: tc.goos, Security: security.run}.ForgetCA(tc.caPath, func() { t.Error("announced a removal, want none") })
			if removed || err != nil || len(security.calls) != tc.calls {
				t.Errorf("ForgetCA = %v, %v after %d calls %q, want nothing removed after %d", removed, err, len(security.calls), security.calls, tc.calls)
			}
			for _, call := range security.calls {
				if call[0] == "delete-certificate" {
					t.Errorf("the security tool was run with %q, want no certificate deleted", call)
				}
			}
		})
	}
}

func TestKeychainForgetCAReportsARemovalThatFailed(t *testing.T) {
	caPath, hash := writeCA(t)
	security := &fakeSecurity{results: map[string]fakeSecurityResult{
		"login-keychain":     printedLoginKeychain,
		"find-certificate":   {output: "SHA-256 hash: " + hash + "\n"},
		"delete-certificate": {output: "Unable to delete certificate matching \"" + hash + "\"\n", err: errNotTrusted},
	}}

	removed, err := Keychain{GOOS: "darwin", Security: security.run}.ForgetCA(caPath, func() {})
	if removed || err == nil || !strings.Contains(err.Error(), "Unable to delete certificate") {
		t.Errorf("ForgetCA = %v, %v, want nothing removed and what the tool printed", removed, err)
	}
}
