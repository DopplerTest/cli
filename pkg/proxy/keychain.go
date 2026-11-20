/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package proxy

import (
	"crypto/sha256"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Keychain is the login keychain of a macOS user, reached through the security tool.
//
// Tools built with Go, such as gh, go and terraform, read no certificate variable on
// macOS. They trust what the system trusts, so they accept the proxy's certificates
// only once the proxy's CA is a trusted root in the keychain. That trust is the user's
// for every program, and it stays after the run that added it.
type Keychain struct {
	// GOOS is the system this process runs on. Only darwin has a keychain.
	GOOS string
	// Security runs the security tool with the arguments. It returns what the tool
	// printed, and an error when the tool did not exit with 0.
	Security func(args ...string) ([]byte, error)
}

// CATrust is what a run knows when it asks for the proxy's CA to be trusted.
type CATrust struct {
	// OnTerminal says someone is there to answer the prompt macOS shows.
	OnTerminal bool
	// Disabled says the user asked for the keychain to be left alone.
	Disabled bool
	// Announce tells the user what the prompt is for. It is called right before the
	// prompt, and not at all when there is none.
	Announce func()
}

// TrustCA makes the CA in the file a trusted root of the login keychain, unless the
// system already trusts it. It reports whether it added the CA. Off macOS, without a
// terminal and when disabled it does nothing.
//
// The CA is trusted under two policies, ssl for TLS and basic for a plain check of a
// chain. With no policy named it would be a root for every use, code signing included,
// while its private key lies in the agent's data directory.
func (k Keychain) TrustCA(caPath string, trust CATrust) (bool, error) {
	if k.GOOS != "darwin" || !trust.OnTerminal || trust.Disabled {
		return false, nil
	}
	// verify-cert exits with 0 only for a certificate the system trusts, and asks the
	// user nothing. It is asked under basic, because under ssl it takes the certificate
	// for a server's and refuses any CA, trusted or not.
	if _, err := k.Security("verify-cert", "-c", caPath, "-p", "basic"); err == nil {
		return false, nil
	}
	keychain, err := k.loginKeychain()
	if err != nil {
		return false, err
	}
	trust.Announce()
	if output, err := k.Security("add-trusted-cert", "-r", "trustRoot", "-p", "ssl", "-p", "basic", "-k", keychain, caPath); err != nil {
		return false, securityError(err, output)
	}
	return true, nil
}

// CAFingerprint returns the SHA-256 hash of the certificate in the file, in the form
// the security tool prints it and takes it: upper-case hex. It returns nothing for a
// file that holds no certificate.
func CAFingerprint(caPath string) string {
	content, err := os.ReadFile(caPath) // #nosec G304
	if err != nil {
		return ""
	}
	certificate, _ := pem.Decode(content)
	if certificate == nil {
		return ""
	}
	return fmt.Sprintf("%X", sha256.Sum256(certificate.Bytes))
}

// ForgetCA removes the CA in the file from the login keychain, with the trust the user
// gave it. It reports whether it removed the CA. The certificate is named by its hash,
// since the CA of every agent has the same name. Off macOS, for a user with no login
// keychain and for a CA the keychain does not hold, it does nothing and announces
// nothing.
func (k Keychain) ForgetCA(caPath string, announce func()) (bool, error) {
	if k.GOOS != "darwin" {
		return false, nil
	}
	hash := CAFingerprint(caPath)
	if hash == "" {
		return false, nil
	}
	// The tool finds no login keychain for a user who has none, such as one whose HOME
	// is a directory made for a test. No run can have added the CA there.
	keychain, err := k.loginKeychain()
	if err != nil {
		return false, nil
	}
	held, err := k.Security("find-certificate", "-a", "-Z", keychain)
	if err != nil {
		return false, securityError(err, held)
	}
	if !strings.Contains(string(held), "SHA-256 hash: "+hash) {
		return false, nil
	}
	announce()
	if output, err := k.Security("delete-certificate", "-Z", hash, "-t", keychain); err != nil {
		return false, securityError(err, output)
	}
	return true, nil
}

// loginKeychain returns the path of the user's login keychain, which the tool prints in
// quotes after some spaces.
func (k Keychain) loginKeychain() (string, error) {
	output, err := k.Security("login-keychain")
	if err != nil {
		return "", securityError(err, output)
	}
	path := strings.Trim(strings.TrimSpace(string(output)), `"`)
	if path == "" {
		return "", errors.New("the security tool named no login keychain")
	}
	return path, nil
}

// securityError adds what the tool printed to its error, which alone is an exit code.
func securityError(err error, output []byte) error {
	if printed := strings.TrimSpace(string(output)); printed != "" {
		return fmt.Errorf("%w: %s", err, printed)
	}
	return err
}
