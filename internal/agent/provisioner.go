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

package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"

	"github.com/go-logr/logr"
)

// scHSMToolPath is the absolute path to sc-hsm-tool (shipped with the opensc package
// in the runtime image). Absolute path mirrors the pcscd manager's convention.
const scHSMToolPath = "/usr/bin/sc-hsm-tool"

// Provisioner initializes a blank SC-HSM token by invoking sc-hsm-tool. It follows the
// same exec-a-binary pattern as PCSCDManager and relies on the pcscd daemon (started
// earlier in agent startup) for PC/SC access.
type Provisioner struct {
	logger logr.Logger
}

// NewProvisioner creates a Provisioner.
func NewProvisioner(logger logr.Logger) *Provisioner {
	return &Provisioner{logger: logger.WithName("provisioner")}
}

// GenerateSOPIN returns a fresh 16-hex-character (8-byte) SC-HSM SO-PIN drawn from
// crypto/rand. The value is meant to be used once for initialization and then discarded
// — it is never logged or persisted.
func GenerateSOPIN() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate SO-PIN: %w", err)
	}
	return strings.ToUpper(hex.EncodeToString(buf)), nil
}

// Provision initializes a blank SC-HSM token: it generates a random SO-PIN, sets the
// user PIN to userPIN, and (optionally) labels the token. The generated SO-PIN is
// discarded when this call returns and is never stored anywhere — recovery from a
// locked user PIN is a flash wipe + re-provision, backed by the K8s Secret mirror.
//
// CAUTION: `sc-hsm-tool --initialize` ERASES the token. Callers MUST first confirm the
// token is blank (hsm.IsTokenBlank) — this function does not re-check.
func (p *Provisioner) Provision(ctx context.Context, userPIN, label string) error {
	if userPIN == "" {
		return fmt.Errorf("user PIN is required to provision a device")
	}

	soPIN, err := GenerateSOPIN()
	if err != nil {
		return err
	}

	args := []string{"--initialize", "--so-pin", soPIN, "--pin", userPIN}
	if label != "" {
		args = append(args, "--label", label)
	}

	// Log only non-sensitive context — never the SO-PIN or user PIN.
	p.logger.Info("Initializing blank SC-HSM token with a random, discarded SO-PIN",
		"tool", scHSMToolPath, "label", label)

	cmd := exec.CommandContext(ctx, scHSMToolPath, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sc-hsm-tool --initialize failed: %w: %s",
			err, redactPINs(out.String(), soPIN, userPIN))
	}

	p.logger.Info("SC-HSM token initialized successfully", "label", label)
	return nil
}

// redactPINs strips PIN values out of tool output before it reaches logs.
func redactPINs(s string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "***")
		}
	}
	return s
}
