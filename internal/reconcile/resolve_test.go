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

package reconcile

import (
	"reflect"
	"testing"
)

// present builds a present (non-tombstone) observation.
func present(id, checksum string, version, timestamp int64) DeviceObservation {
	return DeviceObservation{DeviceID: id, Present: true, Checksum: checksum, Version: version, Timestamp: timestamp}
}

// tombstone builds a tombstone (deleted) observation.
func tombstone(id string, version int64) DeviceObservation {
	return DeviceObservation{DeviceID: id, Tombstone: true, Version: version, Timestamp: 100}
}

// absent builds an observation for a device that lacks the secret entirely.
func absent(id string) DeviceObservation {
	return DeviceObservation{DeviceID: id}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name         string
		obs          []DeviceObservation
		deviceCount  int
		wantChecksum string
		wantDevice   string
		wantDeleted  bool
		wantDiverged bool
		wantStrategy string
		wantOutliers []string
		wantFound    bool
	}{
		{
			name:         "no devices hold the secret",
			obs:          []DeviceObservation{absent("a"), absent("b")},
			deviceCount:  2,
			wantChecksum: "",
			wantStrategy: StrategyNone,
			wantFound:    false,
		},
		{
			name:         "single device present",
			obs:          []DeviceObservation{present("a", "x", 5, 100)},
			deviceCount:  1,
			wantChecksum: "x",
			wantDevice:   "a",
			wantStrategy: StrategyLWW,
			wantFound:    true,
		},
		{
			name:         "single device tombstone",
			obs:          []DeviceObservation{tombstone("a", 5)},
			deviceCount:  1,
			wantChecksum: TombstoneChecksum,
			wantDevice:   "a",
			wantDeleted:  true,
			wantStrategy: StrategyLWW,
			wantFound:    true,
		},
		{
			name:         "two devices agree",
			obs:          []DeviceObservation{present("a", "x", 3, 100), present("b", "x", 3, 100)},
			deviceCount:  2,
			wantChecksum: "x",
			wantDevice:   "a",
			wantStrategy: StrategyLWW,
			wantFound:    true,
		},
		{
			name:         "two devices disagree -> higher version wins",
			obs:          []DeviceObservation{present("a", "old", 3, 100), present("b", "new", 7, 100)},
			deviceCount:  2,
			wantChecksum: "new",
			wantDevice:   "b",
			wantDiverged: true,
			wantStrategy: StrategyLWW,
			wantOutliers: []string{"a"},
			wantFound:    true,
		},
		{
			name:         "two devices disagree, equal version -> higher timestamp wins",
			obs:          []DeviceObservation{present("a", "old", 5, 100), present("b", "new", 5, 200)},
			deviceCount:  2,
			wantChecksum: "new",
			wantDevice:   "b",
			wantDiverged: true,
			wantStrategy: StrategyLWW,
			wantOutliers: []string{"a"},
			wantFound:    true,
		},
		{
			name:         "two devices disagree, equal version+timestamp -> lowest device id wins",
			obs:          []DeviceObservation{present("b", "vb", 5, 100), present("a", "va", 5, 100)},
			deviceCount:  2,
			wantChecksum: "va",
			wantDevice:   "a",
			wantDiverged: true,
			wantStrategy: StrategyLWW,
			wantOutliers: []string{"b"},
			wantFound:    true,
		},
		{
			name: "three devices, 2-1 majority beats a higher-version outlier",
			obs: []DeviceObservation{
				present("a", "x", 1, 100),
				present("b", "x", 1, 100),
				present("c", "y", 99, 999), // newer, but in the minority
			},
			deviceCount:  3,
			wantChecksum: "x",
			wantDevice:   "a",
			wantDiverged: true,
			wantStrategy: StrategyMajority,
			wantOutliers: []string{"c"},
			wantFound:    true,
		},
		{
			name: "three devices all different -> fallback to highest version",
			obs: []DeviceObservation{
				present("a", "x", 1, 100),
				present("b", "y", 5, 100),
				present("c", "z", 3, 100),
			},
			deviceCount:  3,
			wantChecksum: "y",
			wantDevice:   "b",
			wantDiverged: true,
			wantStrategy: StrategyMajorityLWW,
			wantOutliers: []string{"a", "c"},
			wantFound:    true,
		},
		{
			name: "four devices 2v2 split -> no majority -> fallback lww",
			obs: []DeviceObservation{
				present("a", "x", 1, 100),
				present("b", "x", 1, 100),
				present("c", "y", 9, 100),
				present("d", "y", 9, 100),
			},
			deviceCount:  4,
			wantChecksum: "y",
			wantDevice:   "c",
			wantDiverged: true,
			wantStrategy: StrategyMajorityLWW,
			wantOutliers: []string{"a", "b"},
			wantFound:    true,
		},
		{
			name: "five devices 3-2 majority",
			obs: []DeviceObservation{
				present("a", "x", 1, 100),
				present("b", "x", 1, 100),
				present("c", "x", 1, 100),
				present("d", "y", 50, 100),
				present("e", "y", 50, 100),
			},
			deviceCount:  5,
			wantChecksum: "x",
			wantDevice:   "a",
			wantDiverged: true,
			wantStrategy: StrategyMajority,
			wantOutliers: []string{"d", "e"},
			wantFound:    true,
		},
		{
			name: "five devices 2-2-1 split -> no majority -> fallback lww",
			obs: []DeviceObservation{
				present("a", "x", 1, 100),
				present("b", "x", 1, 100),
				present("c", "y", 2, 100),
				present("d", "y", 2, 100),
				present("e", "z", 9, 100),
			},
			deviceCount:  5,
			wantChecksum: "z",
			wantDevice:   "e",
			wantDiverged: true,
			wantStrategy: StrategyMajorityLWW,
			wantOutliers: []string{"a", "b", "c", "d"},
			wantFound:    true,
		},
		{
			name: "deviceCount 3 but only 2 responders that agree -> majority",
			obs: []DeviceObservation{
				present("a", "x", 1, 100),
				present("b", "x", 1, 100),
			},
			deviceCount:  3,
			wantChecksum: "x",
			wantDevice:   "a",
			wantStrategy: StrategyMajority,
			wantFound:    true,
		},
		{
			name: "deviceCount 3 but only 2 responders that disagree -> no majority -> fallback",
			obs: []DeviceObservation{
				present("a", "old", 1, 100),
				present("b", "new", 9, 100),
			},
			deviceCount:  3,
			wantChecksum: "new",
			wantDevice:   "b",
			wantDiverged: true,
			wantStrategy: StrategyMajorityLWW,
			wantOutliers: []string{"a"},
			wantFound:    true,
		},
		{
			name: "tombstone majority deletes",
			obs: []DeviceObservation{
				tombstone("a", 5),
				tombstone("b", 5),
				present("c", "x", 9, 100),
			},
			deviceCount:  3,
			wantChecksum: TombstoneChecksum,
			wantDevice:   "a",
			wantDeleted:  true,
			wantDiverged: true,
			wantStrategy: StrategyMajority,
			wantOutliers: []string{"c"},
			wantFound:    true,
		},
		{
			name: "tombstone minority loses to majority value",
			obs: []DeviceObservation{
				present("a", "x", 1, 100),
				present("b", "x", 1, 100),
				tombstone("c", 9),
			},
			deviceCount:  3,
			wantChecksum: "x",
			wantDevice:   "a",
			wantDeleted:  false,
			wantDiverged: true,
			wantStrategy: StrategyMajority,
			wantOutliers: []string{"c"},
			wantFound:    true,
		},
		{
			name: "absent devices abstain and do not count as divergence",
			obs: []DeviceObservation{
				present("a", "x", 1, 100),
				absent("b"),
			},
			deviceCount:  2,
			wantChecksum: "x",
			wantDevice:   "a",
			wantDiverged: false,
			wantStrategy: StrategyLWW,
			wantFound:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Resolve(tt.obs, tt.deviceCount)
			if got.WinningChecksum != tt.wantChecksum {
				t.Errorf("WinningChecksum = %q, want %q", got.WinningChecksum, tt.wantChecksum)
			}
			if got.WinningDeviceID != tt.wantDevice {
				t.Errorf("WinningDeviceID = %q, want %q", got.WinningDeviceID, tt.wantDevice)
			}
			if got.Deleted != tt.wantDeleted {
				t.Errorf("Deleted = %v, want %v", got.Deleted, tt.wantDeleted)
			}
			if got.Diverged != tt.wantDiverged {
				t.Errorf("Diverged = %v, want %v", got.Diverged, tt.wantDiverged)
			}
			if got.Strategy != tt.wantStrategy {
				t.Errorf("Strategy = %q, want %q", got.Strategy, tt.wantStrategy)
			}
			if got.Found != tt.wantFound {
				t.Errorf("Found = %v, want %v", got.Found, tt.wantFound)
			}
			if len(got.Outliers) != 0 || len(tt.wantOutliers) != 0 {
				if !reflect.DeepEqual(got.Outliers, tt.wantOutliers) {
					t.Errorf("Outliers = %v, want %v", got.Outliers, tt.wantOutliers)
				}
			}
		})
	}
}
