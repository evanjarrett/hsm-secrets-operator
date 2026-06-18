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

package api

// Shared string literals used across the api package. Extracted into constants
// to satisfy the goconst linter and keep map keys, log field names, and
// metadata labels consistent.
const (
	// Response/detail map keys and log field names.
	keyTrue          = "true"
	keyError         = "error"
	keySuccess       = "success"
	keyErrors        = "errors"
	keyDeviceResults = "deviceResults"
	keyPath          = "path"
	keyKey           = "key"
	keyMessage       = "message"
	keyVersion       = "version"

	// Sync metadata label keys (and tombstone value).
	metaSyncDeleted   = "sync.deleted"
	metaSyncTimestamp = "sync.timestamp"
	metaSyncVersion   = "sync.version"

	// HTTP method.
	methodOptions = "OPTIONS"
)
