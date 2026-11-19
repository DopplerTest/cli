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
package utils

import "os"

// jobSignals returns no signal, which stands for all of them. Windows has no job
// control to leave a signal to.
func jobSignals() []os.Signal {
	return nil
}

// inForegroundOfTerminal reports false, since Windows has no foreground job of a
// terminal to be in.
func inForegroundOfTerminal() bool {
	return false
}
