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

// Package reconcile provides the single, shared strategy for resolving which
// copy of a secret wins when multiple HSM devices disagree.
//
// Historically the operator had four independent resolution strategies (the
// HSMSecret controller read devices[0], the proxy used most-recent-wins for
// reads but majority/consensus for checksums, and the mirror used highest
// version). Those disagreed, so the same divergent secret could resolve
// differently depending on who looked. This package collapses them into one
// policy:
//
//   - 1-2 devices: last-write-wins (no majority is possible).
//   - 3+ devices:  majority wins (etcd-style); version/timestamp only break
//     ties or decide when no strict majority exists.
package reconcile

import "sort"

// TombstoneChecksum is the sentinel "value" used so a deleted secret competes
// in the same vote as present copies. A device holding a tombstone votes for
// deletion; whether deletion wins is then just the normal majority/LWW contest.
const TombstoneChecksum = "__deleted__"

// Strategy labels describe how a Decision was reached (for logging/observability).
const (
	StrategyNone        = "none"                  // no device holds the secret
	StrategyLWW         = "lww"                   // last-write-wins (<=2 devices)
	StrategyMajority    = "majority"              // a strict majority agreed (>=3 devices)
	StrategyMajorityLWW = "majority-fallback-lww" // >=3 devices, no majority -> LWW fallback
)

// DeviceObservation is one device's view of a single secret path.
type DeviceObservation struct {
	DeviceID  string // device serial number
	Present   bool   // secret exists on the device and is not a tombstone
	Tombstone bool   // secret is explicitly marked deleted (sync.deleted=true)
	Checksum  string // hsm.CalculateChecksum over the data; "" if absent
	Version   int64  // sync.version (monotonic counter)
	Timestamp int64  // sync.timestamp as unix seconds; secondary tie-break only
}

// hasOpinion reports whether the device holds a copy (real or tombstone) and so
// gets a vote. A device that simply lacks the secret abstains.
func (o DeviceObservation) hasOpinion() bool {
	return o.Present || o.Tombstone
}

// effectiveChecksum is the value the device votes for: the tombstone sentinel
// for deletions, otherwise the real content checksum.
func (o DeviceObservation) effectiveChecksum() string {
	if o.Tombstone {
		return TombstoneChecksum
	}
	return o.Checksum
}

// Decision is the resolved verdict for a secret.
type Decision struct {
	WinningChecksum string   // effective checksum of the winner (TombstoneChecksum if deleted)
	WinningDeviceID string   // a device holding the winner — mirror source / read target
	Deleted         bool     // the winner is a tombstone; readers treat as not-found
	Diverged        bool     // at least one voting device disagrees with the winner
	Strategy        string   // how the winner was chosen (see Strategy* constants)
	Outliers        []string // device IDs that voted differently from the winner (sorted)
	Found           bool     // any device held the secret at all
}

// Resolve picks the authoritative copy of a secret given each device's
// observation and the total number of devices that should hold it.
//
// deviceCount must be the expected device count (e.g. HSMPool.Status.TotalDevices),
// NOT len(obs): a 3-device cluster with one agent briefly down must still use
// majority semantics rather than silently dropping to last-write-wins.
func Resolve(obs []DeviceObservation, deviceCount int) Decision {
	// Only devices that actually hold a copy (real or tombstone) get a vote.
	voters := make([]DeviceObservation, 0, len(obs))
	for _, o := range obs {
		if o.hasOpinion() {
			voters = append(voters, o)
		}
	}

	if len(voters) == 0 {
		return Decision{Strategy: StrategyNone, Found: false}
	}

	// Count votes per distinct effective checksum.
	counts := make(map[string]int)
	for _, v := range voters {
		counts[v.effectiveChecksum()]++
	}

	var winningChecksum string
	var strategy string

	if deviceCount >= 3 {
		// Majority: a checksum held by a strict majority of the EXPECTED devices.
		if cs, ok := strictMajority(counts, deviceCount); ok {
			winningChecksum = cs
			strategy = StrategyMajority
		} else {
			// No consensus — fall back to last-write-wins among the voters.
			winningChecksum = lastWriteWins(voters).effectiveChecksum()
			strategy = StrategyMajorityLWW
		}
	} else {
		// 1-2 devices: majority is impossible, so last-write-wins.
		winningChecksum = lastWriteWins(voters).effectiveChecksum()
		strategy = StrategyLWW
	}

	// Pick a concrete winning device deterministically: the lowest-sorted device
	// ID among those holding the winning value.
	winningDevice := lowestDeviceWithChecksum(voters, winningChecksum)

	// Outliers are voters that disagree with the winner.
	var outliers []string
	for _, v := range voters {
		if v.effectiveChecksum() != winningChecksum {
			outliers = append(outliers, v.DeviceID)
		}
	}
	sort.Strings(outliers)

	return Decision{
		WinningChecksum: winningChecksum,
		WinningDeviceID: winningDevice,
		Deleted:         winningChecksum == TombstoneChecksum,
		Diverged:        len(outliers) > 0,
		Strategy:        strategy,
		Outliers:        outliers,
		Found:           true,
	}
}

// strictMajority returns the checksum whose vote count exceeds deviceCount/2.
// Using deviceCount (not the number of responders) means 2-of-3 agreeing is a
// majority, but 1-of-3 (the other two down) is not.
func strictMajority(counts map[string]int, deviceCount int) (string, bool) {
	threshold := deviceCount / 2 // strict majority needs count > threshold
	// Iterate in deterministic order so ties (impossible for a strict majority,
	// but defensive) resolve predictably.
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if counts[k] > threshold {
			return k, true
		}
	}
	return "", false
}

// lastWriteWins picks the newest voter: highest Version, then highest Timestamp,
// then lowest DeviceID for determinism.
func lastWriteWins(voters []DeviceObservation) DeviceObservation {
	best := voters[0]
	for _, v := range voters[1:] {
		if v.Version > best.Version ||
			(v.Version == best.Version && v.Timestamp > best.Timestamp) ||
			(v.Version == best.Version && v.Timestamp == best.Timestamp && v.DeviceID < best.DeviceID) {
			best = v
		}
	}
	return best
}

// lowestDeviceWithChecksum returns the lowest-sorted device ID holding the given
// effective checksum (deterministic mirror source / read target).
func lowestDeviceWithChecksum(voters []DeviceObservation, checksum string) string {
	winner := ""
	for _, v := range voters {
		if v.effectiveChecksum() != checksum {
			continue
		}
		if winner == "" || v.DeviceID < winner {
			winner = v.DeviceID
		}
	}
	return winner
}
