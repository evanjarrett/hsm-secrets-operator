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

package discovery

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-logr/logr"
)

// ccidPlistGlobs locates the libccid driver's Info.plist across the layouts we
// care about. Debian (both trixie and forky) ships it at /usr/lib/pcsc (symlinked
// to /etc/libccid_Info.plist); some distros/source builds use a multiarch dir,
// /usr/lib64, or /usr/local. The first glob that yields a match wins. Declared as
// a var so tests can override.
var ccidPlistGlobs = []string{
	"/usr/lib/pcsc/drivers/ifd-ccid.bundle/Contents/Info.plist",       // Debian (trixie & forky)
	"/usr/lib/*/pcsc/drivers/ifd-ccid.bundle/Contents/Info.plist",     // multiarch layouts
	"/usr/lib64/pcsc/drivers/ifd-ccid.bundle/Contents/Info.plist",     // Fedora / RPM distros
	"/usr/local/lib/pcsc/drivers/ifd-ccid.bundle/Contents/Info.plist", // source builds
	"/etc/libccid_Info.plist",                                         // Debian canonical editable copy
}

// CCIDAllowlist is the set of {vendorID, productID} pairs that the libccid CCID
// driver (and therefore pcscd) is able to drive. It is the same Info.plist the
// agent's pcscd reads, so it is the single source of truth for "which USB
// devices are usable smartcard/HSM devices".
type CCIDAllowlist struct {
	pairs map[string]bool // key = normalizeID(vid) + ":" + normalizeID(pid)
}

// Contains reports whether the given USB vendor/product ID is a known CCID
// device. IDs are normalized (0x prefix stripped, lowercased) before comparison.
func (a *CCIDAllowlist) Contains(vendorID, productID string) bool {
	if a == nil {
		return false
	}
	return a.pairs[normalizeID(vendorID)+":"+normalizeID(productID)]
}

// Len returns the number of known CCID device pairs.
func (a *CCIDAllowlist) Len() int {
	if a == nil {
		return 0
	}
	return len(a.pairs)
}

// normalizeID canonicalizes a USB ID for comparison: strips a leading 0x/0X,
// trims whitespace, and lowercases. Plist entries look like "0x20A0"; udev
// reports lowercase, unprefixed ("20a0").
func normalizeID(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	return strings.ToLower(s)
}

// LoadCCIDAllowlist locates and parses the libccid Info.plist. A missing or
// unparseable plist is non-fatal: it returns an empty allowlist and logs a
// warning. This keeps local development, the CGO-disabled build, and any future
// libccid layout change working — explicit USB specs still match regardless of
// the allowlist.
func LoadCCIDAllowlist(logger logr.Logger) *CCIDAllowlist {
	empty := &CCIDAllowlist{pairs: map[string]bool{}}

	var path string
	for _, glob := range ccidPlistGlobs {
		matches, err := filepath.Glob(glob)
		if err == nil && len(matches) > 0 {
			path = matches[0]
			break
		}
	}
	if path == "" {
		logger.Info("CCID Info.plist not found; auto-discovery allowlist is empty (explicit usb specs still match)",
			"globs", ccidPlistGlobs)
		return empty
	}

	allowlist, err := loadCCIDAllowlistFromFile(path)
	if err != nil {
		logger.Error(err, "Failed to parse CCID Info.plist; auto-discovery allowlist is empty", "path", path)
		return empty
	}

	logger.Info("Loaded CCID allowlist from Info.plist", "path", path, "pairs", allowlist.Len())
	return allowlist
}

// plistRoot/plistDict/plistNode model just enough of the Apple plist XML to
// recover the three parallel <array>s. A plist <dict> is a flat, order-significant
// sequence of alternating <key> and value elements, so we capture the children in
// document order and pair each <key> with the <array> that follows it.
type plistRoot struct {
	XMLName xml.Name  `xml:"plist"`
	Dict    plistDict `xml:"dict"`
}

type plistDict struct {
	Nodes []plistNode `xml:",any"`
}

type plistNode struct {
	XMLName xml.Name
	Key     string   `xml:",chardata"` // set when the element is <key>
	Strings []string `xml:"string"`    // set when the element is <array>
}

// loadCCIDAllowlistFromFile is the testable core of the parser.
func loadCCIDAllowlistFromFile(path string) (*CCIDAllowlist, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from a fixed glob / test override
	if err != nil {
		return nil, fmt.Errorf("read Info.plist: %w", err)
	}

	var root plistRoot
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse Info.plist xml: %w", err)
	}

	var vendorIDs, productIDs []string
	lastKey := ""
	for _, node := range root.Dict.Nodes {
		switch node.XMLName.Local {
		case "key":
			lastKey = strings.TrimSpace(node.Key)
		case "array":
			switch lastKey {
			case "ifdVendorID":
				vendorIDs = node.Strings
			case "ifdProductID":
				productIDs = node.Strings
			}
			lastKey = ""
		default:
			// Scalar values (e.g. <string> directly under <dict>) reset the key.
			lastKey = ""
		}
	}

	n := min(len(vendorIDs), len(productIDs))

	allowlist := &CCIDAllowlist{pairs: make(map[string]bool, n)}
	for i := range n {
		vid := normalizeID(vendorIDs[i])
		pid := normalizeID(productIDs[i])
		if vid == "" || pid == "" {
			continue
		}
		allowlist.pairs[vid+":"+pid] = true
	}

	return allowlist, nil
}
