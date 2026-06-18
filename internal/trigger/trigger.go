/*
Copyright 2025.

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

// Package trigger holds the neutral interface for requesting a mirror sync.
//
// It is a leaf package (no dependencies on api/controller/mirror) so that any
// component — the REST API handler and the HSMSecret controller alike — can
// request a mirror without importing each other and risking an import cycle.
package trigger

import "sync/atomic"

// MirrorTrigger requests a device-to-device mirror sync. force=true bypasses
// the mirror runnable's debounce/recent-sync skip so divergence converges
// immediately.
type MirrorTrigger interface {
	TriggerMirror(reason, source string, force bool)
}

// global is the process-wide trigger, registered by the mirror runnable at
// startup. It is stored via atomic.Value so concurrent Set/Get are race-free.
var global atomic.Value // holds MirrorTrigger

// Set registers the process-wide mirror trigger. Called once by the mirror
// manager runnable when it starts.
func Set(t MirrorTrigger) {
	global.Store(triggerHolder{t})
}

// Get returns the registered mirror trigger, or nil if none is registered yet.
func Get() MirrorTrigger {
	if v, ok := global.Load().(triggerHolder); ok {
		return v.t
	}
	return nil
}

// triggerHolder boxes the interface so atomic.Value always stores one concrete
// type (atomic.Value panics on inconsistent concrete types).
type triggerHolder struct{ t MirrorTrigger }
