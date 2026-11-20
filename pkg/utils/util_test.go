/*
Copyright © 2019 Doppler <support@doppler.com>

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
package utils

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"testing"
	"time"
)

var username string
var homeDir string
var cwd string

func init() {
	currentUser, err := user.Current()
	if err != nil {
		panic(err)
	}
	username = currentUser.Username

	homeDir, err = os.UserHomeDir()
	if err != nil {
		panic(err)
	}

	cwd, err = filepath.Abs(".")
	if err != nil {
		panic(err)
	}
}

func TestParsePathTilde(t *testing.T) {
	path, err := ParsePath("~")
	if err != nil || path != homeDir {
		t.Error(fmt.Sprintf("Got %s, expected %s", path, homeDir))
	}

	path, err = ParsePath("~/")
	if err != nil || path != homeDir {
		t.Error(fmt.Sprintf("Got %s, expected %s", path, homeDir))
	}

	path, err = ParsePath(fmt.Sprintf("~%s", username))
	if err != nil || path != homeDir {
		t.Error(fmt.Sprintf("Got %s, expected %s", path, homeDir))
	}

	path, err = ParsePath(fmt.Sprintf("~%s/", username))
	if err != nil || path != homeDir {
		t.Error(fmt.Sprintf("Got %s, expected %s", path, homeDir))
	}

	// expect error
	path, err = ParsePath(fmt.Sprintf("~%s/", username+"1"))
	if err == nil || path != "" {
		t.Error(fmt.Sprintf("Got %s, expected error", path))
	}

	path, err = ParsePath("")
	if err == nil || path != "" {
		t.Error(fmt.Sprintf("Got %s, expected error", path))
	}
}

func TestParsePathRelative(t *testing.T) {
	parentDir := filepath.Join(cwd, "..")

	path, err := ParsePath(".")
	if err != nil || path != cwd {
		t.Error(fmt.Sprintf("Got %s, expected %s", path, cwd))
	}

	path, err = ParsePath("./")
	if err != nil || path != cwd {
		t.Error(fmt.Sprintf("Got %s, expected %s", path, cwd))
	}

	path, err = ParsePath("..")
	if err != nil || path != parentDir {
		t.Error(fmt.Sprintf("Got %s, expected %s", path, parentDir))
	}

	path, err = ParsePath("../")
	if err != nil || path != parentDir {
		t.Error(fmt.Sprintf("Got %s, expected %s", path, parentDir))
	}

	path, err = ParsePath("./..")
	if err != nil || path != parentDir {
		t.Error(fmt.Sprintf("Got %s, expected %s", path, parentDir))
	}
}

func TestParsePathAbsolute(t *testing.T) {
	path, err := ParsePath("/")
	if err != nil || path != "/" {
		t.Error(fmt.Sprintf("Got %s, expected %s", path, "/"))
	}

	path, err = ParsePath("/root")
	if err != nil || path != "/root" {
		t.Error(fmt.Sprintf("Got %s, expected %s", path, "/root"))
	}

	path, err = ParsePath("root")
	if err != nil || path != filepath.Join(cwd, "root") {
		t.Error(fmt.Sprintf("Got %s, expected %s", path, filepath.Join(cwd, "root")))
	}
}

func TestGetFilePath(t *testing.T) {
	expected := filepath.Join(cwd, "doppler.env")
	path, err := GetFilePath("./doppler.env")
	if err != nil || path != expected {
		t.Error(fmt.Sprintf("Got %s, expected %s", path, expected))
	}

	path, err = GetFilePath("/root/")
	if err != nil || path != "/root" {
		t.Error(fmt.Sprintf("Got %s, expected %s", path, "/root"))
	}
}

func TestCommandOutputReturnsWhatTheCommandPrintedAndHowItEnded(t *testing.T) {
	if IsWindows() {
		t.Skip("the commands are run with sh")
	}
	output, err := CommandOutput([]string{"sh", "-c", "echo printed; echo an error >&2"}, time.Minute)
	if err != nil || string(output) != "printed\nan error\n" {
		t.Errorf("CommandOutput = %q, %v, want both streams and no error", output, err)
	}

	output, err = CommandOutput([]string{"sh", "-c", "echo refused; exit 3"}, time.Minute)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 || string(output) != "refused\n" {
		t.Errorf("CommandOutput = %q, %v, want what was printed and exit status 3", output, err)
	}

	started := time.Now()
	_, err = CommandOutput([]string{"sleep", "30"}, 50*time.Millisecond)
	if !errors.Is(err, ErrCommandTimedOut) || time.Since(started) > 10*time.Second {
		t.Errorf("CommandOutput = %v after %s, want the command ended when its time was up", err, time.Since(started))
	}
}
