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

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/evanjarrett/hsm-secrets-operator/internal/modes/agent"
	"github.com/evanjarrett/hsm-secrets-operator/internal/modes/discovery"
	"github.com/evanjarrett/hsm-secrets-operator/internal/modes/manager"
)

func main() {
	var mode string
	var logLevel string
	var showHelp bool

	// Create flag set for global flags only
	globalFlags := flag.NewFlagSet("global", flag.ContinueOnError)
	globalFlags.StringVar(&mode, "mode", "", "Operating mode: manager, agent, or discovery (required)")
	globalFlags.StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	globalFlags.BoolVar(&showHelp, "help", false, "Show help")

	// Find the --mode flag and parse only global flags up to that point
	var modeArg string
	var globalArgs []string
	var modeSpecificArgs []string

	args := os.Args[1:]
	i := 0
	for i < len(args) {
		arg := args[i]
		if after, ok := strings.CutPrefix(arg, "--mode="); ok {
			modeArg = after
			globalArgs = append(globalArgs, arg)
			i++
		} else if arg == "--mode" && i+1 < len(args) {
			modeArg = args[i+1]
			globalArgs = append(globalArgs, arg, modeArg)
			i += 2
		} else if strings.HasPrefix(arg, "--log-level=") || arg == "--log-level" ||
			strings.HasPrefix(arg, "--help") || arg == "--help" {
			// These are global flags
			globalArgs = append(globalArgs, arg)
			if arg == "--log-level" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
				globalArgs = append(globalArgs, args[i])
			}
			i++
		} else {
			// This is a mode-specific flag
			modeSpecificArgs = append(modeSpecificArgs, arg)
			i++
		}
	}

	// Parse global flags
	if err := globalFlags.Parse(globalArgs); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing global flags: %v\n", err)
		os.Exit(1)
	}

	// Set mode from parsed value
	mode = modeArg

	// Show help if requested
	if showHelp {
		fmt.Printf("HSM Secrets Operator - Unified Binary\n\n")
		fmt.Printf("Usage:\n")
		fmt.Printf("  %s --mode=<mode> [mode-specific flags]\n\n", os.Args[0])
		fmt.Printf("Modes:\n")
		fmt.Printf("  manager    Run as Kubernetes controller manager\n")
		fmt.Printf("  agent      Run as HSM agent (requires HSM device)\n")
		fmt.Printf("  discovery  Run as device discovery agent\n\n")
		fmt.Printf("Global Flags:\n")
		flag.PrintDefaults()
		fmt.Printf("\nExample:\n")
		fmt.Printf("  %s --mode=manager --help    # Show manager-specific flags\n", os.Args[0])
		fmt.Printf("  %s --mode=agent --help      # Show agent-specific flags\n", os.Args[0])
		fmt.Printf("  %s --mode=discovery --help  # Show discovery-specific flags\n", os.Args[0])
		os.Exit(0)
	}

	// Validate mode
	if mode == "" {
		fmt.Fprintf(os.Stderr, "Error: --mode is required. Valid modes: manager, agent, discovery\n")
		fmt.Fprintf(os.Stderr, "\nUsage:\n")
		fmt.Fprintf(os.Stderr, "  %s --mode=manager    # Run as controller manager\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --mode=agent      # Run as HSM agent\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --mode=discovery  # Run as device discovery\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --help            # Show detailed help\n", os.Args[0])
		os.Exit(1)
	}

	// Set up basic logging for startup message (each mode will configure its own logger)
	ctrl.SetLogger(zap.New(zap.UseDevMode(logLevel == "debug")))

	setupLog := ctrl.Log.WithName("hsm-operator")
	setupLog.Info("Starting HSM Secrets Operator", "mode", mode, "version", "0.0.1")

	// Route to appropriate mode
	switch mode {
	case "manager":
		if err := manager.Run(modeSpecificArgs); err != nil {
			setupLog.Error(err, "Manager mode failed")
			os.Exit(1)
		}
	case "agent":
		if err := agent.Run(modeSpecificArgs, logLevel); err != nil {
			setupLog.Error(err, "Agent mode failed")
			os.Exit(1)
		}
	case "discovery":
		if err := discovery.Run(modeSpecificArgs); err != nil {
			setupLog.Error(err, "Discovery mode failed")
			os.Exit(1)
		}
	default:
		setupLog.Error(fmt.Errorf("invalid mode"), "Unsupported mode", "mode", mode)
		fmt.Fprintf(os.Stderr, "Error: Invalid mode '%s'. Valid modes: manager, agent, discovery\n", mode)
		os.Exit(1)
	}
}
